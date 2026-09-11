package httpapi

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

func (s *Server) listSettlements(w http.ResponseWriter, r *http.Request) {
	limit := queryLimit(r, 100, 500)
	cursorAt, cursorID := requestCursor(r)
	// Settlements are the queue an operator is asked about by name: a seller
	// wants to know where their payout is, and holds are what someone chases.
	args := []any{limit + 1, cursorAt, cursorID}
	filters := ""
	bind := func(value any) string {
		args = append(args, value)
		return "$" + strconv.Itoa(len(args))
	}
	if state := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("state"))); state != "" {
		filters += " AND st.state=" + bind(state)
	}
	if seller, err := uuid.Parse(strings.TrimSpace(r.URL.Query().Get("seller"))); err == nil {
		filters += " AND st.seller_id=" + bind(seller)
	}
	if query := strings.TrimSpace(r.URL.Query().Get("q")); query != "" {
		placeholder := bind(query)
		filters += " AND (o.order_number ILIKE " + placeholder + "||'%' OR u.display_name ILIKE '%'||" + placeholder + "||'%')"
	}
	rows, err := s.DB.Query(r.Context(), `SELECT st.id,st.order_id,o.order_number,st.seller_id,u.display_name,st.gross_amount,st.platform_fee,st.pg_fee,st.tax_amount,st.net_amount,st.state,st.hold_reason,st.scheduled_at,st.settled_at,st.created_at
		FROM settlements st JOIN orders o ON o.id=st.order_id JOIN users u ON u.id=st.seller_id
		WHERE ($2::timestamptz IS NULL OR (st.created_at,st.id) < ($2,$3))`+filters+`
		ORDER BY st.created_at DESC,st.id DESC LIMIT $1`, args...)
	if err != nil {
		writeError(w, 500, "query_failed", "정산을 조회하지 못했습니다.")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id, orderID, sellerID uuid.UUID
		var number, name, state string
		var gross, fee, pgFee, tax, net int64
		var hold *string
		var scheduled, settled *time.Time
		var created time.Time
		if rows.Scan(&id, &orderID, &number, &sellerID, &name, &gross, &fee, &pgFee, &tax, &net, &state, &hold, &scheduled, &settled, &created) == nil {
			items = append(items, map[string]any{"id": id, "order_id": orderID, "order_number": number, "seller_id": sellerID, "seller_name": name, "gross_amount": gross, "platform_fee": fee, "pg_fee": pgFee, "tax_amount": tax, "net_amount": net, "state": state, "hold_reason": hold, "scheduled_at": scheduled, "settled_at": settled, "created_at": created})
		}
	}
	items, next := pageResult(items, limit, func(item map[string]any) (time.Time, uuid.UUID) {
		return timeField(item, "created_at"), uuidField(item, "id")
	})
	writeJSON(w, 200, map[string]any{"items": items, "next_cursor": next})
}

func (s *Server) settlementAction(w http.ResponseWriter, r *http.Request) {
	actor, _ := principalFrom(r.Context())
	id, ok := parseUUIDPath(w, r, "id")
	if !ok {
		return
	}
	var in struct {
		Action string `json:"action"`
		Reason string `json:"reason"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	in.Action = strings.ToLower(in.Action)
	var query string
	var args []any
	switch in.Action {
	case "hold":
		if strings.TrimSpace(in.Reason) == "" {
			writeError(w, 400, "hold_reason_required", "정산 보류 사유가 필요합니다.")
			return
		}
		query = `UPDATE settlements SET state='hold',hold_reason=$2 WHERE id=$1 AND state IN ('scheduled','confirmed')`
		args = []any{id, in.Reason}
	case "release":
		query = `UPDATE settlements SET state='scheduled',hold_reason=NULL WHERE id=$1 AND state='hold'`
		args = []any{id}
	case "complete":
		query = `UPDATE settlements SET state='completed',settled_at=now(),hold_reason=NULL WHERE id=$1 AND state IN ('scheduled','confirmed')`
		args = []any{id}
	default:
		writeError(w, 400, "invalid_action", "hold, release 또는 complete 작업만 가능합니다.")
		return
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		writeError(w, 500, "transaction_failed", "정산 작업을 시작하지 못했습니다.")
		return
	}
	defer tx.Rollback(r.Context()) //nolint:errcheck
	tag, err := tx.Exec(r.Context(), query, args...)
	if err != nil || tag.RowsAffected() == 0 {
		writeError(w, 409, "settlement_state_conflict", "현재 정산 상태에서 이 작업을 수행할 수 없습니다.")
		return
	}
	// Marking a settlement paid without booking it leaves Seller Payable
	// standing as a liability for money that already left the platform, so the
	// payout is recorded the moment the operator confirms it.
	if in.Action == "complete" {
		var orderID uuid.UUID
		var net int64
		var currency string
		if err = tx.QueryRow(r.Context(), `SELECT st.order_id,st.net_amount,o.currency FROM settlements st JOIN orders o ON o.id=st.order_id WHERE st.id=$1`, id).Scan(&orderID, &net, &currency); err != nil {
			writeError(w, 500, "settlement_failed", "정산 정보를 확인하지 못했습니다.")
			return
		}
		if err = writeLedger(r.Context(), tx, uuid.New(), orderID, currency, "판매자 지급 실행",
			debit("Seller Payable", net), credit("Seller Payout", net)); err != nil {
			writeError(w, 500, "settlement_failed", "지급 원장을 기록하지 못했습니다.")
			return
		}
		if err = emitEvent(r.Context(), tx, "settlement", id, "SettlementPaid", map[string]any{"order_id": orderID, "net_amount": net}); err != nil {
			writeError(w, 500, "settlement_failed", "지급 이벤트를 기록하지 못했습니다.")
			return
		}
	}
	// A hold stops a seller's money and a release starts it again. Both used to
	// happen in silence: the seller could see the state change on their
	// earnings page if they happened to look, and was told nothing otherwise.
	// The templates for both events already existed and were never reached
	// from here.
	switch in.Action {
	case "hold":
		if err = emitEvent(r.Context(), tx, "settlement", id, "SettlementHeld", map[string]any{"hold_reason": in.Reason, "actor": actor.UserID}); err != nil {
			writeError(w, 500, "settlement_failed", "정산 보류를 알리지 못했습니다.")
			return
		}
	case "release":
		if err = emitEvent(r.Context(), tx, "settlement", id, "SettlementConfirmed", map[string]any{"actor": actor.UserID}); err != nil {
			writeError(w, 500, "settlement_failed", "정산 재개를 알리지 못했습니다.")
			return
		}
	}
	if err = tx.Commit(r.Context()); err != nil {
		writeError(w, 500, "settlement_failed", "정산 작업을 저장하지 못했습니다.")
		return
	}
	s.audit(r, "settlement."+in.Action, "settlement", id.String(), nil, in, "success")
	writeJSON(w, 200, map[string]any{"ok": true, "action": in.Action})
}

var settlementStateLabels = map[string]string{
	"scheduled": "지급 예정",
	"confirmed": "지급 대기",
	"hold":      "보류",
	"completed": "지급 완료",
	"cancelled": "취소",
}

// listMySettlements is the seller's side of the money. Until now only operators
// could see settlements, which left sellers with no way to answer "what am I
// owed and when does it arrive".
func (s *Server) listMySettlements(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	state := r.URL.Query().Get("state")
	limit := queryLimit(r, 50, 200)
	cursorAt, cursorID := requestCursor(r)
	rows, err := s.DB.Query(r.Context(), `SELECT st.id,st.order_id,o.order_number,t.title,bu.display_name,
		st.gross_amount,st.platform_fee,st.pg_fee,st.tax_amount,st.net_amount,st.state,st.hold_reason,st.scheduled_at,st.settled_at,st.created_at,o.currency
		FROM settlements st JOIN orders o ON o.id=st.order_id JOIN talents t ON t.id=o.talent_id JOIN users bu ON bu.id=o.buyer_id
		WHERE st.seller_id=$1 AND ($2='' OR st.state=$2) AND ($3::timestamptz IS NULL OR (st.created_at,st.id) < ($3,$4))
		ORDER BY st.created_at DESC,st.id DESC LIMIT $5`, p.UserID, state, cursorAt, cursorID, limit+1)
	if err != nil {
		writeError(w, 500, "query_failed", "정산 내역을 조회하지 못했습니다.")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id, orderID uuid.UUID
		var number, title, buyerName, settlementState, currency string
		var gross, fee, pgFee, tax, net int64
		var hold *string
		var scheduled, settled *time.Time
		var created time.Time
		if rows.Scan(&id, &orderID, &number, &title, &buyerName, &gross, &fee, &pgFee, &tax, &net,
			&settlementState, &hold, &scheduled, &settled, &created, &currency) != nil {
			continue
		}
		items = append(items, map[string]any{"id": id, "order_id": orderID, "order_number": number, "talent_title": title,
			"buyer_name": buyerName, "gross_amount": gross, "platform_fee": fee, "pg_fee": pgFee, "tax_amount": tax,
			"net_amount": net, "state": settlementState, "state_label": settlementStateLabels[settlementState],
			"hold_reason": hold, "scheduled_at": scheduled, "settled_at": settled, "created_at": created, "currency": currency})
	}
	items, next := pageResult(items, limit, func(item map[string]any) (time.Time, uuid.UUID) {
		return timeField(item, "created_at"), uuidField(item, "id")
	})
	writeJSON(w, 200, map[string]any{"items": items, "next_cursor": next, "summary": s.sellerEarnings(r, p.UserID)})
}

// sellerEarnings answers the seller's three practical questions: what is coming,
// what is stuck, and what has already arrived.
func (s *Server) sellerEarnings(r *http.Request, seller uuid.UUID) map[string]any {
	var upcoming, held, paid, lifetimeGross, lifetimeFee int64
	var nextPayout *time.Time
	err := s.DB.QueryRow(r.Context(), `SELECT
		COALESCE(sum(net_amount) FILTER (WHERE state IN ('scheduled','confirmed')),0),
		COALESCE(sum(net_amount) FILTER (WHERE state='hold'),0),
		COALESCE(sum(net_amount) FILTER (WHERE state='completed'),0),
		COALESCE(sum(gross_amount) FILTER (WHERE state<>'cancelled'),0),
		COALESCE(sum(platform_fee) FILTER (WHERE state<>'cancelled'),0),
		min(scheduled_at) FILTER (WHERE state IN ('scheduled','confirmed'))
		FROM settlements WHERE seller_id=$1`, seller).Scan(&upcoming, &held, &paid, &lifetimeGross, &lifetimeFee, &nextPayout)
	if err != nil {
		return map[string]any{}
	}
	return map[string]any{"upcoming_amount": upcoming, "held_amount": held, "paid_amount": paid,
		"lifetime_gross": lifetimeGross, "lifetime_fee": lifetimeFee, "next_payout_at": nextPayout}
}
