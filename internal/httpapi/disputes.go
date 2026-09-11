package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// disputeOutcomes maps an operator decision to the label shown to both parties.
var disputeOutcomes = map[string]string{
	"refund_full":       "전액 환불",
	"refund_partial":    "부분 환불",
	"release_to_seller": "판매자 지급",
}

func (s *Server) platformFeeRate(r *http.Request) float64 {
	rate := float64(10)
	if setting, err := s.settingObject(r, "marketplace.policy"); err == nil {
		if value, ok := setting["platform_fee_rate"].(float64); ok && value >= 0 && value <= 100 {
			rate = value
		}
	}
	return rate
}

type ledgerEntry struct {
	Account   string
	Direction string
	Amount    int64
}

func debit(account string, amount int64) ledgerEntry {
	return ledgerEntry{Account: account, Direction: "debit", Amount: amount}
}

func credit(account string, amount int64) ledgerEntry {
	return ledgerEntry{Account: account, Direction: "credit", Amount: amount}
}

var errLedgerUnbalanced = errors.New("ledger transaction is unbalanced")

// writeLedger refuses to write a transaction whose debits and credits differ.
// Escrow money only ever moves through here, so an arithmetic slip fails the
// business transaction instead of silently leaving the books wrong.
func writeLedger(ctx context.Context, tx pgx.Tx, transactionID, orderID uuid.UUID, currency, description string, entries ...ledgerEntry) error {
	var debits, credits int64
	for _, entry := range entries {
		if entry.Amount < 0 {
			return errLedgerUnbalanced
		}
		if entry.Direction == "debit" {
			debits += entry.Amount
		} else {
			credits += entry.Amount
		}
	}
	if debits != credits {
		return errLedgerUnbalanced
	}
	for _, entry := range entries {
		if entry.Amount == 0 {
			continue
		}
		if _, err := tx.Exec(ctx, `INSERT INTO ledger_entries(id,transaction_id,order_id,account,direction,amount,currency,description) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`,
			uuid.New(), transactionID, orderID, entry.Account, entry.Direction, entry.Amount, currency, description); err != nil {
			return err
		}
	}
	return nil
}

// openDispute freezes the order and any scheduled settlement so money cannot
// move while an operator reviews the case.
func (s *Server) openDispute(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	id, ok := parseUUIDPath(w, r, "id")
	if !ok {
		return
	}
	var in struct {
		Reason   string           `json:"reason"`
		Evidence []map[string]any `json:"evidence"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	in.Reason = strings.TrimSpace(in.Reason)
	if len([]rune(in.Reason)) < 5 || len([]rune(in.Reason)) > 2000 {
		writeError(w, 400, "invalid_reason", "분쟁 사유를 5자 이상 2,000자 이하로 입력해 주세요.")
		return
	}
	if in.Evidence == nil {
		in.Evidence = []map[string]any{}
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		writeError(w, 500, "transaction_failed", "분쟁 접수를 시작하지 못했습니다.")
		return
	}
	defer tx.Rollback(r.Context()) //nolint:errcheck
	var buyer, seller uuid.UUID
	var state string
	err = tx.QueryRow(r.Context(), `SELECT buyer_id,seller_id,state FROM orders WHERE id=$1 FOR UPDATE`, id).Scan(&buyer, &seller, &state)
	if err != nil {
		writeError(w, 404, "not_found", "주문을 찾을 수 없습니다.")
		return
	}
	if p.UserID != buyer && p.UserID != seller && !hasPermission(p, "orders.manage") && !hasPermission(p, "risk.manage") {
		writeError(w, 403, "dispute_denied", "이 주문에 분쟁을 접수할 권한이 없습니다.")
		return
	}
	if !orderTransitions[state]["DISPUTED"] {
		writeError(w, 409, "invalid_order_state", "현재 주문 상태에서는 분쟁을 접수할 수 없습니다.")
		return
	}
	var openCount int
	if err = tx.QueryRow(r.Context(), `SELECT count(*) FROM disputes WHERE order_id=$1 AND state IN ('open','under_review')`, id).Scan(&openCount); err != nil {
		writeError(w, 500, "dispute_failed", "분쟁 상태를 확인하지 못했습니다.")
		return
	}
	if openCount > 0 {
		writeError(w, 409, "dispute_already_open", "이미 처리 중인 분쟁이 있습니다.")
		return
	}
	disputeID := uuid.New()
	_, err = tx.Exec(r.Context(), `INSERT INTO disputes(id,order_id,opened_by,reason,evidence) VALUES($1,$2,$3,$4,$5)`, disputeID, id, p.UserID, in.Reason, in.Evidence)
	if err == nil {
		_, err = tx.Exec(r.Context(), `UPDATE settlements SET state='hold',hold_reason='분쟁 접수' WHERE order_id=$1 AND state IN ('scheduled','confirmed')`, id)
	}
	if err == nil {
		err = s.applyOrderTransition(r.Context(), tx, id, p.UserID, state, "DISPUTED", map[string]any{"dispute_id": disputeID})
	}
	if err == nil {
		err = emitEvent(r.Context(), tx, "dispute", disputeID, "DisputeOpened", map[string]any{"order_id": id, "reason": in.Reason, "actor": p.UserID})
	}
	if err != nil {
		writeError(w, 500, "dispute_failed", "분쟁을 접수하지 못했습니다.")
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		writeError(w, 500, "dispute_failed", "분쟁을 접수하지 못했습니다.")
		return
	}
	s.audit(r, "dispute.open", "dispute", disputeID.String(), map[string]any{"state": state}, map[string]any{"order_id": id, "reason": in.Reason}, "success")
	writeJSON(w, 201, map[string]any{"id": disputeID, "order_state": "DISPUTED"})
}

func (s *Server) listOrderDisputes(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	id, ok := parseUUIDPath(w, r, "id")
	if !ok {
		return
	}
	// The organization that paid needs to know a dispute is open on its spend.
	if _, _, _, allowed := s.orderAccess(r, id, p); !allowed && !hasPermission(p, "risk.manage") && !s.organizationManagerOfOrder(r.Context(), id, p.UserID) {
		writeError(w, 403, "order_access_denied", "이 주문의 분쟁을 볼 수 없습니다.")
		return
	}
	rows, err := s.DB.Query(r.Context(), `SELECT d.id,d.order_id,d.opened_by,u.display_name,d.state,d.reason,d.evidence,d.resolution,d.created_at,d.updated_at
		FROM disputes d JOIN users u ON u.id=d.opened_by WHERE d.order_id=$1 ORDER BY d.created_at DESC`, id)
	if err != nil {
		writeError(w, 500, "query_failed", "분쟁을 조회하지 못했습니다.")
		return
	}
	defer rows.Close()
	writeJSON(w, 200, map[string]any{"items": scanDisputes(rows, false)})
}

// listAdminDisputes is a work queue, and a work queue that shows the newest
// item first starves the oldest one. Unresolved disputes are therefore ordered
// oldest first: the person who has been waiting longest with their money frozen
// is the person the next operator should be looking at. Resolved rows keep the
// newest first ordering, because there the useful view is recent history.
func (s *Server) listAdminDisputes(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	args := []any{queryLimit(r, 100, 500)}
	filter := ""
	if state != "" {
		args = append(args, state)
		filter = " AND d.state=$2"
	}
	// A queue that does not show which cases someone has already looked at is a
	// queue that invites the same investigation twice, which is the thing the
	// notes exist to prevent.
	rows, err := s.DB.Query(r.Context(), `SELECT d.id,d.order_id,d.opened_by,u.display_name,d.state,d.reason,d.evidence,d.resolution,d.created_at,d.updated_at,
		o.order_number,o.state,o.amount,o.currency,t.title,b.display_name,se.display_name,
		(SELECT count(*) FROM operator_notes n WHERE n.subject_type='dispute' AND n.subject_id=d.id)
		FROM disputes d JOIN users u ON u.id=d.opened_by JOIN orders o ON o.id=d.order_id JOIN talents t ON t.id=o.talent_id
		JOIN users b ON b.id=o.buyer_id JOIN users se ON se.id=o.seller_id
		WHERE true`+filter+`
		ORDER BY CASE d.state WHEN 'open' THEN 1 WHEN 'under_review' THEN 2 ELSE 3 END,
			CASE WHEN d.state IN ('open','under_review') THEN d.created_at END ASC,
			d.created_at DESC
		LIMIT $1`, args...)
	if err != nil {
		writeError(w, 500, "query_failed", "분쟁을 조회하지 못했습니다.")
		return
	}
	defer rows.Close()
	writeJSON(w, 200, map[string]any{"items": scanDisputes(rows, true), "outcomes": disputeOutcomes, "sla_hours": s.disputeSLAHours(r)})
}

// disputeSLAHours is how long a dispute may sit unresolved before the console
// calls it out. It is a promise to the two people whose money is frozen while
// it waits, so it is a setting an operator can tighten.
func (s *Server) disputeSLAHours(r *http.Request) int {
	// sla.policy already existed and already had a dispute response target in
	// it. Adding a second one to marketplace.policy meant an operator could set
	// the number that reads like the right one and change nothing at all.
	policy, err := s.settingObject(r, "sla.policy")
	if err != nil {
		return 24
	}
	return intSetting(policy, "dispute_response_hours", 24)
}

func scanDisputes(rows pgx.Rows, withOrder bool) []map[string]any {
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id, orderID, openedBy uuid.UUID
		var openedName, state, reason string
		var evidenceRaw, resolutionRaw []byte
		var created, updated time.Time
		var number, orderState, currency, talentTitle, buyerName, sellerName string
		var amount int64
		var noteCount int64
		var err error
		if withOrder {
			err = rows.Scan(&id, &orderID, &openedBy, &openedName, &state, &reason, &evidenceRaw, &resolutionRaw, &created, &updated, &number, &orderState, &amount, &currency, &talentTitle, &buyerName, &sellerName, &noteCount)
		} else {
			err = rows.Scan(&id, &orderID, &openedBy, &openedName, &state, &reason, &evidenceRaw, &resolutionRaw, &created, &updated)
		}
		if err != nil {
			continue
		}
		var evidence, resolution any
		_ = json.Unmarshal(evidenceRaw, &evidence)
		_ = json.Unmarshal(resolutionRaw, &resolution)
		// How long someone has been waiting is the queue's most important
		// column, so it travels with the row instead of being recomputed by
		// every client that renders it.
		waiting := 0.0
		if state == "open" || state == "under_review" {
			waiting = time.Since(created).Hours()
		}
		item := map[string]any{"id": id, "order_id": orderID, "opened_by": openedBy, "opened_by_name": openedName, "state": state, "reason": reason, "evidence": evidence, "resolution": resolution, "created_at": created, "updated_at": updated, "waiting_hours": int(waiting), "note_count": noteCount}
		if withOrder {
			item["order_number"] = number
			item["order_state"] = orderState
			item["amount"] = amount
			item["currency"] = currency
			item["talent_title"] = talentTitle
			item["buyer_name"] = buyerName
			item["seller_name"] = sellerName
		}
		items = append(items, item)
	}
	return items
}

// resolveDispute is the only place that unwinds escrow. It reverses whatever
// settlement the order already had, books the refund, and re-books the seller
// share for whatever the buyer does not get back, so every order's ledger keeps
// summing to zero regardless of when the dispute was opened.
func (s *Server) resolveDispute(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	id, ok := parseUUIDPath(w, r, "id")
	if !ok {
		return
	}
	var in struct {
		Outcome      string `json:"outcome"`
		RefundAmount int64  `json:"refund_amount"`
		Note         string `json:"note"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	label, known := disputeOutcomes[in.Outcome]
	if !known {
		writeError(w, 400, "invalid_outcome", "refund_full, refund_partial 또는 release_to_seller 중에서 선택해 주세요.")
		return
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		writeError(w, 500, "transaction_failed", "분쟁 처리를 시작하지 못했습니다.")
		return
	}
	defer tx.Rollback(r.Context()) //nolint:errcheck
	var orderID, buyer, seller uuid.UUID
	var disputeState, orderState, currency string
	var amount, discount int64
	err = tx.QueryRow(r.Context(), `SELECT d.order_id,d.state,o.buyer_id,o.seller_id,o.state,o.amount,o.currency,o.discount_amount
		FROM disputes d JOIN orders o ON o.id=d.order_id WHERE d.id=$1 FOR UPDATE`, id).Scan(&orderID, &disputeState, &buyer, &seller, &orderState, &amount, &currency, &discount)
	if err != nil {
		writeError(w, 404, "dispute_not_found", "분쟁을 찾을 수 없습니다.")
		return
	}
	if disputeState != "open" && disputeState != "under_review" {
		writeError(w, 409, "dispute_already_resolved", "이미 처리된 분쟁입니다.")
		return
	}
	if orderState != "DISPUTED" {
		writeError(w, 409, "invalid_order_state", "분쟁 상태의 주문만 처리할 수 있습니다.")
		return
	}
	refund := int64(0)
	switch in.Outcome {
	case "refund_full":
		refund = amount
	case "refund_partial":
		refund = in.RefundAmount
		if refund <= 0 || refund >= amount {
			writeError(w, 400, "invalid_refund_amount", "부분 환불 금액은 0보다 크고 결제 금액보다 작아야 합니다.")
			return
		}
	}
	if err = s.unwindSettlement(r, tx, orderID, currency); err != nil {
		if errors.Is(err, errSettlementCompleted) {
			writeError(w, 409, "settlement_already_completed", "이미 지급 완료된 정산은 분쟁으로 되돌릴 수 없습니다. 별도 회수 절차가 필요합니다.")
			return
		}
		writeError(w, 500, "dispute_failed", "정산을 되돌리지 못했습니다.")
		return
	}
	buyerRefund, promotionRefund, refundID, err := bookRefund(r.Context(), tx, orderID, buyer, p.UserID, refund, amount, discount, currency, label+" · 분쟁 처리", "분쟁 환불")
	if err != nil {
		writeError(w, 500, "dispute_failed", "환불을 기록하지 못했습니다.")
		return
	}
	remaining := amount - refund
	if remaining > 0 {
		fee := int64(float64(remaining) * s.platformFeeRate(r) / 100)
		net := remaining - fee
		settlementID := uuid.New()
		_, err = tx.Exec(r.Context(), `INSERT INTO settlements(id,order_id,seller_id,gross_amount,platform_fee,net_amount,state,scheduled_at) VALUES($1,$2,$3,$4,$5,$6,'scheduled',now()+interval '3 days')
			ON CONFLICT(order_id) DO UPDATE SET gross_amount=EXCLUDED.gross_amount,platform_fee=EXCLUDED.platform_fee,net_amount=EXCLUDED.net_amount,state='scheduled',hold_reason=NULL,scheduled_at=EXCLUDED.scheduled_at`,
			settlementID, orderID, seller, remaining, fee, net)
		if err == nil {
			err = writeLedger(r.Context(), tx, uuid.New(), orderID, currency, "분쟁 처리 정산", debit("Escrow", remaining), credit("Platform Revenue", fee), credit("Seller Payable", net))
		}
		if err != nil {
			writeError(w, 500, "dispute_failed", "정산을 다시 만들지 못했습니다.")
			return
		}
	}
	to := "COMPLETED"
	if refund == amount {
		to = "REFUNDED"
	}
	resolution := map[string]any{"outcome": in.Outcome, "label": label, "refund_amount": refund, "buyer_refund_amount": buyerRefund, "promotion_refund_amount": promotionRefund, "seller_amount": remaining, "note": strings.TrimSpace(in.Note), "decided_by": p.UserID}
	_, err = tx.Exec(r.Context(), `UPDATE disputes SET state='resolved',resolution=$2,assigned_to=$3,updated_at=now() WHERE id=$1`, id, resolution, p.UserID)
	if err == nil {
		err = s.applyOrderTransition(r.Context(), tx, orderID, p.UserID, orderState, to, map[string]any{"dispute_id": id, "outcome": in.Outcome, "refund_amount": refund})
	}
	if err == nil {
		err = emitEvent(r.Context(), tx, "dispute", id, "DisputeResolved", map[string]any{"order_id": orderID, "outcome": in.Outcome, "outcome_label": label, "refund_amount": refund, "seller_amount": remaining, "actor": p.UserID})
	}
	if err != nil {
		writeError(w, 500, "dispute_failed", "분쟁 처리를 저장하지 못했습니다.")
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		writeError(w, 500, "dispute_failed", "분쟁 처리를 저장하지 못했습니다.")
		return
	}
	s.audit(r, "dispute.resolve", "dispute", id.String(), map[string]any{"state": disputeState}, resolution, "success")
	writeJSON(w, 200, map[string]any{"ok": true, "order_state": to, "refund_amount": refund, "buyer_refund_amount": buyerRefund, "promotion_refund_amount": promotionRefund, "seller_amount": remaining, "refund_id": refundID})
}

var errSettlementCompleted = errors.New("settlement already completed")

// unwindSettlement reverses the ledger entries a purchase confirmation booked
// so the escrow balance is available again for the dispute decision.
func (s *Server) unwindSettlement(r *http.Request, tx pgx.Tx, orderID uuid.UUID, currency string) error {
	var settlementID uuid.UUID
	var gross, fee, net int64
	var state string
	err := tx.QueryRow(r.Context(), `SELECT id,gross_amount,platform_fee,net_amount,state FROM settlements WHERE order_id=$1 FOR UPDATE`, orderID).Scan(&settlementID, &gross, &fee, &net, &state)
	if err == pgx.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	if state == "completed" {
		return errSettlementCompleted
	}
	if state == "cancelled" {
		return nil
	}
	if _, err = tx.Exec(r.Context(), `UPDATE settlements SET state='cancelled',hold_reason='분쟁 처리로 취소' WHERE id=$1`, settlementID); err != nil {
		return err
	}
	return writeLedger(r.Context(), tx, uuid.New(), orderID, currency, "분쟁 처리 정산 취소",
		debit("Platform Revenue", fee), debit("Seller Payable", net), credit("Escrow", gross))
}

// bookRefund moves money out of escrow back to whoever funded it. A coupon
// means the buyer paid less than the order was worth, so only their share
// returns to them and the rest unwinds the promotion. The budget release
// follows the buyer's share for the same reason.
func bookRefund(ctx context.Context, tx pgx.Tx, orderID, buyer, actor uuid.UUID, refund, amount, discount int64, currency, reason, description string) (int64, int64, *uuid.UUID, error) {
	if refund <= 0 {
		return 0, 0, nil, nil
	}
	buyerRefund := refund
	if discount > 0 && amount > 0 {
		buyerRefund = refund * (amount - discount) / amount
	}
	promotionRefund := refund - buyerRefund
	var refundID *uuid.UUID
	if buyerRefund > 0 {
		newRefund := uuid.New()
		refundID = &newRefund
		var paymentID *uuid.UUID
		var found uuid.UUID
		if tx.QueryRow(ctx, `SELECT id FROM payments WHERE order_id=$1 AND state='captured' ORDER BY created_at DESC LIMIT 1`, orderID).Scan(&found) == nil {
			paymentID = &found
		}
		if _, err := tx.Exec(ctx, `INSERT INTO refunds(id,order_id,payment_id,requested_by,amount,reason,state,decided_by,decided_at) VALUES($1,$2,$3,$4,$5,$6,'completed',$7,now())`,
			newRefund, orderID, paymentID, buyer, buyerRefund, reason, nullableUUID(actor)); err != nil {
			return 0, 0, nil, err
		}
	}
	if err := writeLedger(ctx, tx, uuid.New(), orderID, currency, description,
		debit("Escrow", refund), credit("Buyer Refund", buyerRefund), credit("Promotion Recovery", promotionRefund)); err != nil {
		return 0, 0, nil, err
	}
	if err := releaseBudget(ctx, tx, orderID, buyerRefund); err != nil {
		return 0, 0, nil, err
	}
	return buyerRefund, promotionRefund, refundID, nil
}

// escrowBalance is what the order still holds. Deriving it from the ledger
// rather than the order state means a cancellation returns exactly what is
// there, whatever path the order took to get here.
func escrowBalance(ctx context.Context, tx pgx.Tx, orderID uuid.UUID) (int64, error) {
	var held int64
	err := tx.QueryRow(ctx, `SELECT COALESCE(sum(CASE WHEN direction='credit' THEN amount ELSE -amount END),0) FROM ledger_entries WHERE order_id=$1 AND account='Escrow'`, orderID).Scan(&held)
	if held < 0 {
		held = 0
	}
	return held, err
}

// getAdminDispute is the evidence an operator should have before they split
// someone's money. The queue showed the reason the opener typed and the order
// number, and nothing else: no deliveries, no conversation, no timeline, no
// sense of whether either party has been here before. Deciding a refund from
// one side's account of events is the wrong way to decide a refund.
//
// The parts are assembled here rather than left to the console to stitch, so
// that what the operator saw is one thing with one answer.
func (s *Server) getAdminDispute(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDPath(w, r, "id")
	if !ok {
		return
	}
	var raw []byte
	err := s.DB.QueryRow(r.Context(), `SELECT jsonb_build_object(
			'id',d.id,'state',d.state,'reason',d.reason,'evidence',d.evidence,'resolution',d.resolution,
			'created_at',d.created_at,'updated_at',d.updated_at,
			'opened_by',jsonb_build_object('id',ob.id,'display_name',ob.display_name,
				'side',CASE WHEN d.opened_by=o.buyer_id THEN 'buyer' ELSE 'seller' END),
			'order',jsonb_build_object('id',o.id,'order_number',o.order_number,'state',o.state,
				'amount',o.amount,'discount_amount',o.discount_amount,'currency',o.currency,
				'requirements',o.requirements,'due_at',o.due_at,'created_at',o.created_at,
				'overdue_days',GREATEST(0,FLOOR(EXTRACT(EPOCH FROM (now()-o.due_at))/86400)),
				'talent_title',t.title,
				'escrow',(SELECT COALESCE(sum(CASE WHEN direction='debit' THEN -amount ELSE amount END),0)
					FROM ledger_entries WHERE order_id=o.id AND account='Escrow'),
				-- An accepted order that is then disputed has already moved its
				-- escrow into a settlement, so the escrow line alone reads as
				-- "there is no money here" at the exact moment someone decides
				-- where the money goes. What matters is what resolving the
				-- dispute can actually pull back.
				'settlement_state',(SELECT st.state FROM settlements st WHERE st.order_id=o.id),
				'reclaimable',(SELECT COALESCE(sum(CASE WHEN direction='debit' THEN -amount ELSE amount END),0)
					FROM ledger_entries WHERE order_id=o.id AND account='Escrow')
					+ COALESCE((SELECT st.gross_amount FROM settlements st WHERE st.order_id=o.id AND st.state IN ('scheduled','confirmed','hold')),0)),
			'buyer',jsonb_build_object('id',b.id,'display_name',b.display_name,
				'orders',(SELECT count(*) FROM orders x WHERE x.buyer_id=b.id),
				'disputes_opened',(SELECT count(*) FROM disputes x WHERE x.opened_by=b.id)),
			'seller',jsonb_build_object('id',se.id,'display_name',se.display_name,
				'orders',(SELECT count(*) FROM orders x WHERE x.seller_id=se.id),
				'disputes_against',(SELECT count(*) FROM disputes x JOIN orders xo ON xo.id=x.order_id
					WHERE xo.seller_id=se.id AND x.opened_by<>se.id),
				'rating',COALESCE(sp.rating,0),'level',COALESCE(sp.level,'NEW')),
			'timeline',COALESCE((SELECT jsonb_agg(jsonb_build_object('at',tl.created_at,'event',tl.event_type,
					'from',tl.from_state,'to',tl.to_state,'note',tl.data->>'note') ORDER BY tl.created_at)
				FROM order_timeline tl WHERE tl.order_id=o.id),'[]'::jsonb),
			'deliveries',COALESCE((SELECT jsonb_agg(jsonb_build_object('at',dl.created_at,'type',dl.delivery_type,
					'description',dl.description,'content',dl.content) ORDER BY dl.created_at)
				FROM deliveries dl WHERE dl.order_id=o.id),'[]'::jsonb),
			'messages',COALESCE((SELECT jsonb_agg(entry ORDER BY entry->>'at')
				FROM (SELECT jsonb_build_object('at',m.created_at,'sender',mu.display_name,
						'side',CASE WHEN m.sender_id=o.buyer_id THEN 'buyer' ELSE 'seller' END,
						'body',m.body) AS entry
					FROM messages m JOIN users mu ON mu.id=m.sender_id
					WHERE m.order_id=o.id ORDER BY m.created_at DESC LIMIT 100) recent),'[]'::jsonb)
		) FROM disputes d
		JOIN orders o ON o.id=d.order_id
		JOIN talents t ON t.id=o.talent_id
		JOIN users ob ON ob.id=d.opened_by
		JOIN users b ON b.id=o.buyer_id
		JOIN users se ON se.id=o.seller_id
		LEFT JOIN seller_profiles sp ON sp.user_id=o.seller_id
		WHERE d.id=$1`, id).Scan(&raw)
	if err == pgx.ErrNoRows {
		writeError(w, 404, "dispute_not_found", "분쟁을 찾을 수 없습니다.")
		return
	}
	if err != nil {
		// Reporting a broken query as "not found" sends an operator looking for
		// a case that is sitting right in front of them.
		s.Logger.Error("admin dispute detail failed", "error", err.Error(), "dispute", id.String())
		writeError(w, 500, "query_failed", "분쟁 정보를 조회하지 못했습니다.")
		return
	}
	var payload any
	_ = json.Unmarshal(raw, &payload)
	writeJSON(w, 200, payload)
}
