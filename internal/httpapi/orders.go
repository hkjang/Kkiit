package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var orderTransitions = map[string]map[string]bool{
	"CREATED":             {"PAYMENT_PENDING": true, "CANCELLED": true},
	"PAYMENT_PENDING":     {"PAID": true, "CANCELLED": true},
	"PAID":                {"REQUIREMENT_PENDING": true, "READY": true, "REFUNDED": true},
	"REQUIREMENT_PENDING": {"READY": true, "CANCEL_REQUESTED": true},
	"READY":               {"IN_PROGRESS": true, "CANCEL_REQUESTED": true},
	"IN_PROGRESS":         {"DELIVERED": true, "CANCEL_REQUESTED": true, "DISPUTED": true},
	"DELIVERED":           {"REVISION_REQUESTED": true, "ACCEPTED": true, "DISPUTED": true},
	"REVISION_REQUESTED":  {"IN_PROGRESS": true, "DELIVERED": true, "DISPUTED": true},
	"ACCEPTED":            {"COMPLETED": true, "DISPUTED": true},
	"CANCEL_REQUESTED":    {"CANCELLED": true, "IN_PROGRESS": true},
	"DISPUTED":            {"REFUNDED": true, "COMPLETED": true},
}

func (s *Server) createOrder(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	var in struct {
		TalentID       uuid.UUID        `json:"talent_id"`
		PackageID      *uuid.UUID       `json:"package_id,omitempty"`
		Requirements   map[string]any   `json:"requirements"`
		Options        []map[string]any `json:"options"`
		CouponCode     string           `json:"coupon_code,omitempty"`
		OrganizationID *uuid.UUID       `json:"organization_id,omitempty"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	var sellerID uuid.UUID
	var talentPrice int64
	var currency string
	var talentDays int
	var packagePrice *int64
	var packageDays *int
	err := s.DB.QueryRow(r.Context(), `SELECT t.seller_id,t.base_price,t.currency,t.delivery_days,p.price,p.delivery_days FROM talents t JOIN users su ON su.id=t.seller_id LEFT JOIN talent_packages p ON p.id=$2 AND p.talent_id=t.id AND p.active WHERE t.id=$1 AND t.status='published' AND su.status='active'`, in.TalentID, in.PackageID).Scan(&sellerID, &talentPrice, &currency, &talentDays, &packagePrice, &packageDays)
	if err != nil {
		writeError(w, 404, "talent_not_available", "주문 가능한 상품을 찾을 수 없습니다.")
		return
	}
	if sellerID == p.UserID {
		writeError(w, 400, "self_order_not_allowed", "본인의 상품은 주문할 수 없습니다.")
		return
	}
	price := talentPrice
	days := talentDays
	if in.PackageID != nil {
		if packagePrice == nil {
			writeError(w, 400, "package_not_available", "선택한 패키지를 주문할 수 없습니다.")
			return
		}
		price = *packagePrice
		days = *packageDays
	}
	// Option prices come from the product, never from the buyer's request. The
	// client used to be able to name its own add on price.
	optionIDs := make([]uuid.UUID, 0, len(in.Options))
	for _, option := range in.Options {
		raw, _ := option["id"].(string)
		parsed, parseErr := uuid.Parse(raw)
		if parseErr != nil {
			writeError(w, 400, "invalid_option", "추가 옵션은 상품에 등록된 옵션 식별자로 지정해 주세요.")
			return
		}
		optionIDs = append(optionIDs, parsed)
	}
	selectedOptions := make([]map[string]any, 0, len(optionIDs))
	if len(optionIDs) > 0 {
		rows, optionErr := s.DB.Query(r.Context(), `SELECT id,name,price,additional_days FROM talent_options WHERE talent_id=$1 AND active AND id=ANY($2)`, in.TalentID, optionIDs)
		if optionErr != nil {
			writeError(w, 500, "query_failed", "추가 옵션을 확인하지 못했습니다.")
			return
		}
		for rows.Next() {
			var optionID uuid.UUID
			var name string
			var optionPrice int64
			var extraDays int
			if rows.Scan(&optionID, &name, &optionPrice, &extraDays) != nil {
				continue
			}
			price += optionPrice
			days += extraDays
			selectedOptions = append(selectedOptions, map[string]any{"id": optionID, "name": name, "price": optionPrice, "additional_days": extraDays})
		}
		rows.Close()
		if len(selectedOptions) != len(optionIDs) {
			writeError(w, 400, "invalid_option", "선택한 추가 옵션 중 판매 중이 아닌 항목이 있습니다.")
			return
		}
	}
	id := uuid.New()
	number := fmt.Sprintf("KK-%s-%s", time.Now().Format("20060102"), strings.ToUpper(strings.ReplaceAll(uuid.NewString()[:8], "-", "")))
	due := time.Now().AddDate(0, 0, days)
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		writeError(w, 500, "transaction_failed", "주문을 시작하지 못했습니다.")
		return
	}
	defer tx.Rollback(r.Context())
	// Capacity used to be checked before the transaction opened, so any number
	// of buyers could pass the same check at the same moment and a seller who
	// said they can handle one order at a time woke up owing five. The check now
	// runs behind a lock on the seller's own profile row, which serialises
	// concurrent orders for that seller and nobody else.
	if full, capacity, err := s.sellerAtCapacityTx(r.Context(), tx, sellerID); err != nil {
		writeError(w, 500, "capacity_check_failed", "판매자의 진행 상황을 확인하지 못했습니다.")
		return
	} else if full {
		writeError(w, 409, "seller_at_capacity", fmt.Sprintf("이 판매자는 동시에 진행할 수 있는 주문 %d건을 모두 소화하고 있습니다. 잠시 후 다시 시도해 주세요.", capacity))
		return
	}
	requirements, message, ok := normalizeRequirements(r.Context(), tx, in.TalentID, in.Requirements, true)
	if !ok {
		writeError(w, 400, "requirements_incomplete", message)
		return
	}
	coupon, message, ok := resolveCoupon(r.Context(), tx, in.CouponCode, p.UserID, price)
	if !ok {
		writeError(w, 409, "coupon_not_applicable", message)
		return
	}
	var couponID any
	if coupon.ID != uuid.Nil {
		couponID = coupon.ID
	}
	var organizationID any
	var budgetID any
	var reserved int64
	if in.OrganizationID != nil {
		if memberRole(r.Context(), tx, *in.OrganizationID, p.UserID) == "" {
			writeError(w, 403, "organization_membership_required", "이 조직으로 주문할 권한이 없습니다.")
			return
		}
		// Turning the enterprise feature off has to stop company spending, not
		// just hide the section from the menu. Every organization route refuses
		// while it is off, and so does ordering on a company's account.
		if !s.featureEnabled(r.Context(), "enterprise") {
			writeError(w, 404, "feature_disabled", "조직 계정 기능이 비활성화되어 있습니다.")
			return
		}
		organizationID = *in.OrganizationID
		var budgetMessage string
		var reserveOK bool
		var exhausted uuid.UUID
		budgetID, reserved, budgetMessage, reserveOK, exhausted = reserveBudget(r.Context(), tx, *in.OrganizationID, price-coupon.Discount)
		if !reserveOK {
			// The rollback has to happen before the announcement, not after it:
			// this transaction holds a lock on the budget row, and the
			// announcement reads that row on its own connection. Deferring the
			// announcement would run it first and deadlock against the lock the
			// same request still holds.
			_ = tx.Rollback(r.Context())
			s.announceBudgetExhausted(r.Context(), exhausted)
			writeError(w, 409, "budget_exceeded", budgetMessage)
			return
		}
	}
	_, err = tx.Exec(r.Context(), `INSERT INTO orders(id,order_number,buyer_id,seller_id,talent_id,package_id,state,amount,currency,requirements,due_at,metadata,coupon_id,discount_amount,organization_id,budget_id,budget_consumed) VALUES($1,$2,$3,$4,$5,$6,'CREATED',$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`, id, number, p.UserID, sellerID, in.TalentID, in.PackageID, price, currency, requirements, due, map[string]any{"options": selectedOptions}, couponID, coupon.Discount, organizationID, budgetID, reserved)
	if err == nil && coupon.ID != uuid.Nil {
		_, err = tx.Exec(r.Context(), `INSERT INTO coupon_redemptions(id,coupon_id,user_id,order_id,discount_amount) VALUES($1,$2,$3,$4,$5)`, uuid.New(), coupon.ID, p.UserID, id, coupon.Discount)
	}
	if err == nil {
		_, err = tx.Exec(r.Context(), `INSERT INTO order_timeline(id,order_id,event_type,actor_user_id,to_state,data) VALUES($1,$2,'OrderCreated',$3,'CREATED',$4)`, uuid.New(), id, p.UserID, map[string]any{"amount": price, "currency": currency, "discount_amount": coupon.Discount})
	}
	if err == nil {
		err = emitEvent(r.Context(), tx, "order", id, "OrderCreated", map[string]any{"buyer_id": p.UserID, "seller_id": sellerID, "actor": p.UserID})
	}
	if err != nil {
		writeError(w, 500, "order_failed", "주문을 만들지 못했습니다.")
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		writeError(w, 500, "order_failed", "주문을 만들지 못했습니다.")
		return
	}
	s.audit(r, "order.create", "order", id.String(), nil, map[string]any{"number": number, "amount": price, "discount_amount": coupon.Discount, "coupon": coupon.Code}, "success")
	writeJSON(w, 201, map[string]any{"id": id, "order_number": number, "state": "CREATED", "amount": price, "discount_amount": coupon.Discount, "payable_amount": price - coupon.Discount, "currency": currency, "due_at": due, "organization_id": organizationID, "budget_reserved": reserved})
}

// listOrders had two problems that grew together. A flat LIMIT 100 meant a
// buyer past their hundredth order simply could not reach the older ones, and
// the ownership test carried an `OR $2` operator bypass. That bypass is the
// expensive part: pgx prepares statements, so PostgreSQL switches to a generic
// plan once a statement is reused, and a generic plan cannot fold the boolean
// away. Measured on 300k orders, the generic plan was a 12.7ms parallel
// sequential scan of the whole table where the same request without the bypass
// was a 0.24ms bitmap scan of the caller's own two indexes. Choosing the
// ownership branch in Go keeps the operator behaviour and the fast plan.
func (s *Server) listOrders(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	limit := queryLimit(r, 50, 200)
	cursorAt, cursorID := requestCursor(r)
	const columns = `SELECT o.id,o.order_number,o.state,o.amount,o.currency,o.due_at,o.created_at,t.title,o.buyer_id,o.seller_id,bu.display_name,su.display_name
		FROM orders o JOIN talents t ON t.id=o.talent_id JOIN users bu ON bu.id=o.buyer_id JOIN users su ON su.id=o.seller_id WHERE `
	const keyset = ` AND ($3::timestamptz IS NULL OR (o.created_at,o.id) < ($3,$4)) ORDER BY o.created_at DESC,o.id DESC LIMIT $2`
	args := []any{p.UserID, limit + 1, cursorAt, cursorID}
	// An operator opening this list still sees every order, as they always have.
	// A seller who also buys sees both sides mixed together, and once the list
	// pages properly there is no way to find the one order that is in dispute
	// without walking the whole history. Both filters are chosen from fixed
	// sets, so nothing a caller sends reaches the query text.
	filters := ""
	switch r.URL.Query().Get("role") {
	case "buyer":
		filters = ` AND o.buyer_id=$1`
	case "seller":
		filters = ` AND o.seller_id=$1`
	}
	if state := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("state"))); state != "" {
		if _, known := orderTransitions[state]; known || state == "COMPLETED" || state == "CANCELLED" || state == "REFUNDED" {
			args = append(args, state)
			filters += ` AND o.state=$` + strconv.Itoa(len(args))
		} else if state == "OPEN" {
			// "진행 중" is what someone actually looks for, and it is not one
			// state but every state before an order is finished with.
			filters += ` AND o.state NOT IN ('COMPLETED','CANCELLED','REFUNDED')`
		}
	}
	ownership := `(o.buyer_id=$1 OR o.seller_id=$1)`
	if hasPermission(p, "orders.manage") {
		// The parameter is kept in the predicate so both branches take the same
		// argument list; the cast is what tells PostgreSQL its type.
		ownership = `($1::uuid IS NOT NULL)`
	}
	rows, err := s.DB.Query(r.Context(), columns+ownership+filters+keyset, args...)
	if err != nil {
		writeError(w, 500, "query_failed", "주문을 조회하지 못했습니다.")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id, buyer, seller uuid.UUID
		var number, state, currency, title, buyerName, sellerName string
		var amount int64
		var due *time.Time
		var created time.Time
		if rows.Scan(&id, &number, &state, &amount, &currency, &due, &created, &title, &buyer, &seller, &buyerName, &sellerName) == nil {
			items = append(items, map[string]any{"id": id, "order_number": number, "state": state, "amount": amount, "currency": currency, "due_at": due, "created_at": created, "talent_title": title, "buyer": map[string]any{"id": buyer, "display_name": buyerName}, "seller": map[string]any{"id": seller, "display_name": sellerName}})
		}
	}
	items, next := pageResult(items, limit, func(item map[string]any) (time.Time, uuid.UUID) {
		return timeField(item, "created_at"), uuidField(item, "id")
	})
	writeJSON(w, 200, map[string]any{"items": items, "next_cursor": next})
}

func (s *Server) payOrder(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	id, ok := parseUUIDPath(w, r, "id")
	if !ok {
		return
	}
	idempotencyKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if idempotencyKey == "" || len(idempotencyKey) > 200 {
		writeError(w, 400, "idempotency_key_required", "Idempotency-Key 헤더가 필요합니다.")
		return
	}
	policy, err := s.settingObject(r, "payment.policy")
	if err != nil {
		writeError(w, 503, "payment_policy_unavailable", "결제 정책을 확인하지 못했습니다.")
		return
	}
	provider, _ := policy["provider"].(string)
	if provider == "" {
		provider = "manual"
	}
	if provider != "manual" {
		writeError(w, 501, "payment_adapter_required", "선택한 결제사의 Adapter가 아직 연결되지 않았습니다.")
		return
	}
	tx, err := s.DB.BeginTx(r.Context(), pgx.TxOptions{})
	if err != nil {
		writeError(w, 500, "payment_failed", "결제를 시작하지 못했습니다.")
		return
	}
	defer tx.Rollback(r.Context()) //nolint:errcheck
	var buyer uuid.UUID
	var state, currency string
	var amount, discount int64
	err = tx.QueryRow(r.Context(), `SELECT buyer_id,state,amount,currency,discount_amount FROM orders WHERE id=$1 FOR UPDATE`, id).Scan(&buyer, &state, &amount, &currency, &discount)
	if err != nil || buyer != p.UserID && !hasPermission(p, "orders.manage") {
		writeError(w, 403, "payment_denied", "이 주문을 결제할 수 없습니다.")
		return
	}
	payable := amount - discount
	var existingID uuid.UUID
	if err = tx.QueryRow(r.Context(), `SELECT id FROM payments WHERE idempotency_key=$1 AND order_id=$2`, idempotencyKey, id).Scan(&existingID); err == nil {
		writeJSON(w, 200, map[string]any{"payment_id": existingID, "state": state, "idempotent_replay": true})
		return
	} else if err != pgx.ErrNoRows {
		writeError(w, 500, "payment_failed", "결제 상태를 확인하지 못했습니다.")
		return
	}
	if state != "CREATED" && state != "PAYMENT_PENDING" {
		writeError(w, 409, "invalid_order_state", "현재 주문 상태에서는 결제할 수 없습니다.")
		return
	}
	if state == "CREATED" {
		if err = s.applyOrderTransition(r.Context(), tx, id, p.UserID, "CREATED", "PAYMENT_PENDING", map[string]any{"provider": provider}); err != nil {
			writeError(w, 500, "payment_failed", "결제 상태를 저장하지 못했습니다.")
			return
		}
		state = "PAYMENT_PENDING"
	}
	paymentID := uuid.New()
	_, err = tx.Exec(r.Context(), `INSERT INTO payments(id,order_id,provider,provider_reference,state,amount,currency,idempotency_key,metadata) VALUES($1,$2,$3,$4,'captured',$5,$6,$7,$8)`, paymentID, id, provider, "manual:"+paymentID.String(), payable, currency, idempotencyKey, map[string]any{"order_amount": amount, "discount_amount": discount})
	if err == nil {
		// Escrow always holds the full order value; a coupon shifts part of it
		// from the buyer to the platform's promotion expense.
		err = writeLedger(r.Context(), tx, uuid.New(), id, currency, "선결제 에스크로 보관",
			debit("Buyer Payment", payable), debit("Promotion Expense", discount), credit("Escrow", amount))
	}
	if err == nil {
		err = s.applyOrderTransition(r.Context(), tx, id, p.UserID, state, "PAID", map[string]any{"payment_id": paymentID, "provider": provider})
	}
	if err == nil {
		err = s.applyOrderTransition(r.Context(), tx, id, p.UserID, "PAID", "READY", map[string]any{"requirements_collected": true})
	}
	if err != nil {
		writeError(w, 500, "payment_failed", "결제를 완료하지 못했습니다.")
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		writeError(w, 500, "payment_failed", "결제를 완료하지 못했습니다.")
		return
	}
	s.audit(r, "payment.capture", "payment", paymentID.String(), nil, map[string]any{"order_id": id, "amount": payable, "discount_amount": discount, "provider": provider}, "success")
	writeJSON(w, 200, map[string]any{"payment_id": paymentID, "state": "READY", "amount": payable, "order_amount": amount, "discount_amount": discount, "currency": currency})
}

// getOrder is readable by the order's parties, platform operators, and the
// managers of the organization that funded it. An organization admin could see
// the order in their organization list but not open it, which left them
// accountable for spend they could not inspect. The conversation stays private
// to the two parties; what the company sees is the order, its timeline and what
// was delivered.
func (s *Server) getOrder(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	id, ok := parseUUIDPath(w, r, "id")
	if !ok {
		return
	}
	var raw []byte
	err := s.DB.QueryRow(r.Context(), `SELECT jsonb_build_object('id',o.id,'order_number',o.order_number,'state',o.state,'amount',o.amount,'discount_amount',o.discount_amount,'payable_amount',o.amount-o.discount_amount,'coupon_code',(SELECT c.code FROM coupons c WHERE c.id=o.coupon_id),'currency',o.currency,'requirements',o.requirements,'due_at',o.due_at,'overdue',(o.due_at IS NOT NULL AND o.due_at < now() AND o.state IN ('PAID','REQUIREMENT_PENDING','READY','IN_PROGRESS','REVISION_REQUESTED','CANCEL_REQUESTED')),'overdue_days',GREATEST(0,FLOOR(EXTRACT(EPOCH FROM (now()-o.due_at))/86400)),'accepted_at',o.accepted_at,'completed_at',o.completed_at,'created_at',o.created_at,'buyer',jsonb_build_object('id',bu.id,'display_name',bu.display_name),'seller',jsonb_build_object('id',su.id,'display_name',su.display_name),'talent',jsonb_build_object('id',t.id,'title',t.title),'timeline',COALESCE((SELECT jsonb_agg(to_jsonb(tl) ORDER BY tl.created_at) FROM order_timeline tl WHERE tl.order_id=o.id),'[]'::jsonb),'deliveries',COALESCE((SELECT jsonb_agg(to_jsonb(d) ORDER BY d.version) FROM deliveries d WHERE d.order_id=o.id),'[]'::jsonb),'revisions',COALESCE((SELECT jsonb_agg(to_jsonb(rv) ORDER BY rv.revision_number) FROM revisions rv WHERE rv.order_id=o.id),'[]'::jsonb)) FROM orders o JOIN users bu ON bu.id=o.buyer_id JOIN users su ON su.id=o.seller_id JOIN talents t ON t.id=o.talent_id WHERE o.id=$1 AND (o.buyer_id=$2 OR o.seller_id=$2 OR $3
		OR EXISTS(SELECT 1 FROM organization_users ou WHERE ou.organization_id=o.organization_id AND ou.user_id=$2 AND ou.role IN ('owner','admin')))`, id, p.UserID, hasPermission(p, "orders.manage")).Scan(&raw)
	if err != nil {
		writeError(w, 404, "not_found", "주문을 찾을 수 없습니다.")
		return
	}
	var result any
	_ = json.Unmarshal(raw, &result)
	writeJSON(w, 200, result)
}

// overdueGraceDays is how long after the promised date a seller still has
// before the buyer can end the order themselves. Zero disables the path
// entirely, leaving every cancellation with an operator.
func (s *Server) overdueGraceDays(r *http.Request) int {
	policy, err := s.settingObject(r, "marketplace.policy")
	if err != nil {
		return 7
	}
	return intSetting(policy, "overdue_cancel_days", 7)
}

func (s *Server) overdueBeyondGrace(r *http.Request, due *time.Time) bool {
	days := s.overdueGraceDays(r)
	if due == nil || days <= 0 {
		return false
	}
	return time.Now().After(due.AddDate(0, 0, days))
}

func canTransition(p Principal, buyer, seller uuid.UUID, to string) bool {
	if hasPermission(p, "orders.manage") {
		return true
	}
	switch to {
	case "PAYMENT_PENDING", "CANCEL_REQUESTED", "ACCEPTED", "REVISION_REQUESTED":
		return p.UserID == buyer
	case "READY", "IN_PROGRESS", "DELIVERED":
		return p.UserID == seller
	default:
		return false
	}
}

func (s *Server) transitionOrder(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	id, ok := parseUUIDPath(w, r, "id")
	if !ok {
		return
	}
	var in struct {
		To   string `json:"to"`
		Note string `json:"note"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	in.To = strings.ToUpper(in.To)
	tx, err := s.DB.BeginTx(r.Context(), pgx.TxOptions{})
	if err != nil {
		writeError(w, 500, "transaction_failed", "상태 변경을 시작하지 못했습니다.")
		return
	}
	defer tx.Rollback(r.Context())
	var from string
	var buyer, seller uuid.UUID
	var due *time.Time
	err = tx.QueryRow(r.Context(), `SELECT state,buyer_id,seller_id,due_at FROM orders WHERE id=$1 FOR UPDATE`, id).Scan(&from, &buyer, &seller, &due)
	if err != nil {
		writeError(w, 404, "not_found", "주문을 찾을 수 없습니다.")
		return
	}
	// A buyer may walk away from an order that blew through its deadline and
	// never arrived. Cancelling otherwise needs an operator, which is right when
	// there is a disagreement, but a seller who delivered nothing well past the
	// date they promised is not a disagreement — and until now their silence was
	// enough to keep the buyer's money in escrow indefinitely.
	overdueCancel := p.UserID == buyer && in.To == "CANCELLED" && from == "CANCEL_REQUESTED" && s.overdueBeyondGrace(r, due)
	if !orderTransitions[from][in.To] {
		writeError(w, 409, "invalid_transition", fmt.Sprintf("%s 상태에서 %s 상태로 변경할 수 없습니다.", from, in.To))
		return
	}
	if !overdueCancel && !canTransition(p, buyer, seller, in.To) {
		if p.UserID == buyer && in.To == "CANCELLED" {
			writeError(w, 403, "cancellation_pending_review", fmt.Sprintf("납기 후 %d일이 지나야 직접 취소할 수 있습니다. 그전에는 운영자가 확인합니다.", s.overdueGraceDays(r)))
			return
		}
		writeError(w, 403, "transition_denied", "이 상태 변경을 수행할 권한이 없습니다.")
		return
	}
	// A disputed order's money decision belongs to dispute resolution, which
	// knows how to split the refund and the seller's share. Completing it here
	// would leave the escrow balance stranded.
	if from == "DISPUTED" && in.To == "COMPLETED" {
		writeError(w, 409, "resolve_dispute_first", "분쟁 중인 주문은 분쟁 처리로 종료해 주세요. 그래야 환불과 정산이 함께 기록됩니다.")
		return
	}
	if in.To == "ACCEPTED" {
		// Confirming a purchase must book the settlement, so this path runs the
		// same code as the dedicated endpoint instead of only moving the state.
		var amount int64
		var currency string
		if err = tx.QueryRow(r.Context(), `SELECT amount,currency FROM orders WHERE id=$1`, id).Scan(&amount, &currency); err != nil {
			writeError(w, 500, "transition_failed", "주문 금액을 확인하지 못했습니다.")
			return
		}
		if _, _, _, err = s.acceptAndSettle(r.Context(), tx, id, p.UserID, seller, from, amount, currency, s.platformFeeRate(r)); err != nil {
			writeError(w, 500, "transition_failed", "구매확정 정산을 만들지 못했습니다.")
			return
		}
	} else if err = s.applyOrderTransition(r.Context(), tx, id, p.UserID, from, in.To, map[string]any{"note": in.Note}); err != nil {
		writeError(w, 500, "transition_failed", "주문 상태를 변경하지 못했습니다.")
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		writeError(w, 500, "transition_failed", "주문 상태를 변경하지 못했습니다.")
		return
	}
	s.audit(r, "order.transition", "order", id.String(), map[string]any{"state": from}, map[string]any{"state": in.To, "note": in.Note}, "success")
	writeJSON(w, 200, map[string]any{"state": in.To})
}

// acceptAndSettle books the purchase confirmation: the state change, the
// settlement row and the ledger entries that move escrow to the seller. It
// takes a context so the auto accept job can run the identical path.
func (s *Server) acceptAndSettle(ctx context.Context, tx pgx.Tx, id, actor, seller uuid.UUID, from string, amount int64, currency string, feeRate float64) (uuid.UUID, int64, int64, error) {
	fee := int64(float64(amount) * feeRate / 100)
	net := amount - fee
	if err := s.applyOrderTransition(ctx, tx, id, actor, from, "ACCEPTED", map[string]any{"platform_fee": fee}); err != nil {
		return uuid.Nil, 0, 0, err
	}
	settlementID := uuid.New()
	_, err := tx.Exec(ctx, `INSERT INTO settlements(id,order_id,seller_id,gross_amount,platform_fee,net_amount,scheduled_at) VALUES($1,$2,$3,$4,$5,$6,now()+interval '3 days')`, settlementID, id, seller, amount, fee, net)
	if err == nil {
		err = writeLedger(ctx, tx, uuid.New(), id, currency, "구매확정 정산 예정", debit("Escrow", amount), credit("Platform Revenue", fee), credit("Seller Payable", net))
	}
	if err == nil {
		err = emitEvent(ctx, tx, "settlement", settlementID, "SettlementCreated", map[string]any{"order_id": id, "net_amount": net, "gross_amount": amount, "platform_fee": fee})
	}
	return settlementID, fee, net, err
}

func (s *Server) applyOrderTransition(ctx context.Context, tx pgx.Tx, id, actor uuid.UUID, from, to string, data map[string]any) error {
	if to == "CANCELLED" || to == "REFUNDED" {
		// Ending an order must not strand the buyer's money in escrow. Whatever
		// escrow still holds for this order goes back, and the organization's
		// budget follows the buyer's share.
		held, err := escrowBalance(ctx, tx, id)
		if err != nil {
			return err
		}
		if held > 0 {
			var buyer uuid.UUID
			var amount, discount int64
			var currency string
			if err := tx.QueryRow(ctx, `SELECT buyer_id,amount,discount_amount,currency FROM orders WHERE id=$1`, id).Scan(&buyer, &amount, &discount, &currency); err != nil {
				return err
			}
			if _, _, _, err := bookRefund(ctx, tx, id, buyer, actor, held, amount, discount, currency, "주문 "+to+" 환불", "주문 종료 환불"); err != nil {
				return err
			}
		}
		// Anything the order still holds after the refund is budget that was
		// never spent, such as an order cancelled before payment.
		if err := releaseBudget(ctx, tx, id, 0); err != nil {
			return err
		}
	}
	_, err := tx.Exec(ctx, `UPDATE orders SET state=$2,accepted_at=CASE WHEN $2='ACCEPTED' THEN now() ELSE accepted_at END,completed_at=CASE WHEN $2='COMPLETED' THEN now() ELSE completed_at END,updated_at=now() WHERE id=$1`, id, to)
	if err == nil {
		// A background transition has no person behind it, so the actor stays
		// null rather than pointing at a user that does not exist.
		_, err = tx.Exec(ctx, `INSERT INTO order_timeline(id,order_id,event_type,actor_user_id,from_state,to_state,data) VALUES($1,$2,$3,$4,$5,$6,$7)`, uuid.New(), id, "Order"+to, nullableUUID(actor), from, to, data)
	}
	if err == nil {
		payload := map[string]any{"from": from, "to": to, "actor": actor}
		for key, value := range data {
			if _, exists := payload[key]; !exists {
				payload[key] = value
			}
		}
		err = emitEvent(ctx, tx, "order", id, "Order"+to, payload)
	}
	return err
}

func (s *Server) createDelivery(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	id, ok := parseUUIDPath(w, r, "id")
	if !ok {
		return
	}
	var in struct {
		DeliveryType string         `json:"delivery_type"`
		Content      map[string]any `json:"content"`
		Description  string         `json:"description"`
		ContentHash  string         `json:"content_hash"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.DeliveryType == "" || in.Content == nil {
		writeError(w, 400, "invalid_delivery", "납품 유형과 내용을 입력해 주세요.")
		return
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		writeError(w, 500, "transaction_failed", "납품을 시작하지 못했습니다.")
		return
	}
	defer tx.Rollback(r.Context())
	var seller uuid.UUID
	var state string
	var version int
	err = tx.QueryRow(r.Context(), `SELECT seller_id,state,(SELECT count(*)+1 FROM deliveries WHERE order_id=$1) FROM orders WHERE id=$1 FOR UPDATE`, id).Scan(&seller, &state, &version)
	if err != nil || seller != p.UserID && !hasPermission(p, "orders.manage") {
		writeError(w, 403, "delivery_denied", "이 주문을 납품할 권한이 없습니다.")
		return
	}
	if state != "IN_PROGRESS" && state != "REVISION_REQUESTED" {
		writeError(w, 409, "invalid_order_state", "작업 중이거나 수정 요청 상태에서만 납품할 수 있습니다.")
		return
	}
	deliveryID := uuid.New()
	_, err = tx.Exec(r.Context(), `INSERT INTO deliveries(id,order_id,seller_id,version,delivery_type,content,description,content_hash) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, deliveryID, id, p.UserID, version, in.DeliveryType, in.Content, in.Description, nullableString(in.ContentHash))
	if err == nil {
		err = s.applyOrderTransition(r.Context(), tx, id, p.UserID, state, "DELIVERED", map[string]any{"delivery_id": deliveryID, "version": version})
	}
	if err != nil {
		writeError(w, 500, "delivery_failed", "납품을 저장하지 못했습니다.")
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		writeError(w, 500, "delivery_failed", "납품을 저장하지 못했습니다.")
		return
	}
	s.audit(r, "delivery.create", "delivery", deliveryID.String(), nil, map[string]any{"order_id": id, "version": version}, "success")
	writeJSON(w, 201, map[string]any{"id": deliveryID, "version": version, "order_state": "DELIVERED"})
}

func (s *Server) createRevision(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	id, ok := parseUUIDPath(w, r, "id")
	if !ok {
		return
	}
	var in struct {
		DeliveryID  *uuid.UUID       `json:"delivery_id,omitempty"`
		Details     string           `json:"details"`
		Priority    string           `json:"priority"`
		Attachments []map[string]any `json:"attachments"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.Details) == "" {
		writeError(w, 400, "details_required", "수정 내용을 입력해 주세요.")
		return
	}
	if in.Priority == "" {
		in.Priority = "normal"
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		writeError(w, 500, "transaction_failed", "수정 요청을 시작하지 못했습니다.")
		return
	}
	defer tx.Rollback(r.Context())
	var buyer uuid.UUID
	var state string
	var number, included int
	err = tx.QueryRow(r.Context(), `SELECT o.buyer_id,o.state,(SELECT count(*)+1 FROM revisions WHERE order_id=o.id),
		COALESCE(p.revision_count,t.revision_count,0)
		FROM orders o JOIN talents t ON t.id=o.talent_id LEFT JOIN talent_packages p ON p.id=o.package_id
		WHERE o.id=$1 FOR UPDATE OF o`, id).Scan(&buyer, &state, &number, &included)
	if err != nil || buyer != p.UserID && !hasPermission(p, "orders.manage") {
		writeError(w, 403, "revision_denied", "이 주문에 수정 요청할 권한이 없습니다.")
		return
	}
	if state != "DELIVERED" {
		writeError(w, 409, "invalid_order_state", "납품 완료 상태에서만 수정 요청할 수 있습니다.")
		return
	}
	// Unlimited revisions let a buyer hold a seller indefinitely. The product's
	// included count is the promise; the platform ceiling is the backstop.
	limit := included
	if policy, settingErr := s.settingObject(r, "marketplace.policy"); settingErr == nil {
		if value, ok := policy["max_revision_count"].(float64); ok && value >= 0 && (limit <= 0 || int(value) < limit) {
			limit = int(value)
		}
	}
	if limit > 0 && number > limit {
		writeError(w, 409, "revision_limit_reached", fmt.Sprintf("이 주문에 포함된 수정 요청 %d회를 모두 사용했습니다. 구매확정하거나 분쟁으로 접수해 주세요.", limit))
		return
	}
	if in.Attachments == nil {
		// Same trap as the message body: the column is NOT NULL with a default
		// and the field is optional, so omitting it sent SQL NULL and failed as
		// a server error instead of being the empty list it means.
		in.Attachments = []map[string]any{}
	}
	revisionID := uuid.New()
	_, err = tx.Exec(r.Context(), `INSERT INTO revisions(id,order_id,delivery_id,revision_number,details,priority,attachments) VALUES($1,$2,$3,$4,$5,$6,$7)`, revisionID, id, in.DeliveryID, number, in.Details, in.Priority, in.Attachments)
	if err == nil {
		err = s.applyOrderTransition(r.Context(), tx, id, p.UserID, state, "REVISION_REQUESTED", map[string]any{"revision_id": revisionID, "revision_number": number})
	}
	if err != nil {
		writeError(w, 500, "revision_failed", "수정 요청을 저장하지 못했습니다.")
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		writeError(w, 500, "revision_failed", "수정 요청을 저장하지 못했습니다.")
		return
	}
	s.audit(r, "revision.create", "revision", revisionID.String(), nil, map[string]any{"order_id": id, "number": number}, "success")
	writeJSON(w, 201, map[string]any{"id": revisionID, "revision_number": number, "revision_limit": limit, "order_state": "REVISION_REQUESTED"})
}

func (s *Server) acceptOrder(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	id, ok := parseUUIDPath(w, r, "id")
	if !ok {
		return
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		writeError(w, 500, "transaction_failed", "구매확정을 시작하지 못했습니다.")
		return
	}
	defer tx.Rollback(r.Context())
	var buyer, seller uuid.UUID
	var state, currency string
	var amount int64
	err = tx.QueryRow(r.Context(), `SELECT buyer_id,seller_id,state,amount,currency FROM orders WHERE id=$1 FOR UPDATE`, id).Scan(&buyer, &seller, &state, &amount, &currency)
	if err != nil || buyer != p.UserID && !hasPermission(p, "orders.manage") {
		writeError(w, 403, "accept_denied", "이 주문을 구매확정할 권한이 없습니다.")
		return
	}
	if state != "DELIVERED" {
		writeError(w, 409, "invalid_order_state", "납품 완료 상태에서만 구매확정할 수 있습니다.")
		return
	}
	settlementID, fee, net, err := s.acceptAndSettle(r.Context(), tx, id, p.UserID, seller, state, amount, currency, s.platformFeeRate(r))
	if err != nil {
		writeError(w, 500, "settlement_failed", "구매확정 정산을 생성하지 못했습니다.")
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		writeError(w, 500, "accept_failed", "구매확정을 저장하지 못했습니다.")
		return
	}
	s.audit(r, "order.accept", "order", id.String(), map[string]any{"state": state}, map[string]any{"state": "ACCEPTED", "settlement_id": settlementID}, "success")
	writeJSON(w, 200, map[string]any{"state": "ACCEPTED", "settlement_id": settlementID, "gross_amount": amount, "platform_fee": fee, "net_amount": net})
}

// sellerAtCapacity reports whether a seller already has as many live orders as
// they said they can handle. Capacity was declared on the profile but never
// enforced, so a seller could be buried under work they cannot deliver. A
// capacity of zero means the seller has not set a limit.
// sellerAtCapacityTx is the same question asked where the answer can be relied
// on. FOR UPDATE on the seller's profile makes two buyers ordering from the
// same seller at the same instant take turns; buyers of other sellers are not
// affected. A seller with no profile row has no declared limit, which is the
// same as no limit.
func (s *Server) sellerAtCapacityTx(ctx context.Context, tx pgx.Tx, seller uuid.UUID) (bool, int, error) {
	var capacity int
	err := tx.QueryRow(ctx, `SELECT COALESCE(capacity,0) FROM seller_profiles WHERE user_id=$1 FOR UPDATE`, seller).Scan(&capacity)
	if err == pgx.ErrNoRows {
		return false, 0, nil
	}
	if err != nil {
		return false, 0, err
	}
	if capacity <= 0 {
		return false, 0, nil
	}
	var active int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM orders WHERE seller_id=$1 AND state NOT IN ('COMPLETED','CANCELLED','REFUNDED')`, seller).Scan(&active); err != nil {
		return false, 0, err
	}
	return active >= capacity, capacity, nil
}

// requirementLimits bound what a buyer can put into an order brief. The blob is
// stored as it arrives, so without a ceiling one request can write an arbitrary
// amount into the orders table.
const (
	maxRequirementValue = 5000
	maxRequirementTotal = 40000
)

// normalizeRequirements enforces the order form the seller defined and stores
// the answers under their labels.
//
// Nothing checked this before. A seller could mark a field required and still
// receive an order with an empty brief, because the requirement list only ever
// existed in the client. The seller is then holding a deadline and a payment
// with nothing to work from, which is how an order becomes a dispute.
//
// The answers arrive keyed by requirement id from the web client and by label
// from other callers, so both are accepted. They are stored by label because an
// order has to stay readable years later, when the requirement rows behind
// those ids may have been edited or deleted.
// requireAll is false for an order made from an accepted quote: the buyer
// already wrote the brief in the request for quotation and the seller priced
// the work against it, so demanding the listing's generic form again would ask
// them to describe the same job twice.
func normalizeRequirements(ctx context.Context, tx pgx.Tx, talentID uuid.UUID, answers map[string]any, requireAll bool) (map[string]any, string, bool) {
	rows, err := tx.Query(ctx, `SELECT id,label,required FROM talent_requirements WHERE talent_id=$1 ORDER BY sort_order,label`, talentID)
	if err != nil {
		return nil, "주문 양식을 확인하지 못했습니다.", false
	}
	defer rows.Close()
	type field struct {
		id       uuid.UUID
		label    string
		required bool
	}
	fields := make([]field, 0)
	for rows.Next() {
		var item field
		if rows.Scan(&item.id, &item.label, &item.required) == nil {
			fields = append(fields, item)
		}
	}
	if rows.Err() != nil {
		return nil, "주문 양식을 확인하지 못했습니다.", false
	}
	normalized := make(map[string]any, len(fields))
	total := 0
	for _, item := range fields {
		raw, ok := answers[item.id.String()]
		if !ok {
			raw, ok = answers[item.label]
		}
		value := ""
		if ok {
			value = strings.TrimSpace(fmt.Sprint(raw))
		}
		if value == "" {
			if item.required && requireAll {
				return nil, fmt.Sprintf("%s 항목을 입력해 주세요.", item.label), false
			}
			continue
		}
		if len([]rune(value)) > maxRequirementValue {
			return nil, fmt.Sprintf("%s 항목이 너무 깁니다.", item.label), false
		}
		total += len(value)
		if total > maxRequirementTotal {
			return nil, "요구사항이 너무 깁니다. 파일 첨부를 이용해 주세요.", false
		}
		normalized[item.label] = value
	}
	return normalized, "", true
}
