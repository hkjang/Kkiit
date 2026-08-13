package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
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
		TalentID     uuid.UUID        `json:"talent_id"`
		PackageID    *uuid.UUID       `json:"package_id,omitempty"`
		Requirements map[string]any   `json:"requirements"`
		Options      []map[string]any `json:"options"`
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
	err := s.DB.QueryRow(r.Context(), `SELECT t.seller_id,t.base_price,t.currency,t.delivery_days,p.price,p.delivery_days FROM talents t LEFT JOIN talent_packages p ON p.id=$2 AND p.talent_id=t.id AND p.active WHERE t.id=$1 AND t.status='published'`, in.TalentID, in.PackageID).Scan(&sellerID, &talentPrice, &currency, &talentDays, &packagePrice, &packageDays)
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
	for _, option := range in.Options {
		value, ok := option["price"].(float64)
		if !ok || value < 0 {
			writeError(w, 400, "invalid_option", "추가 옵션 가격을 확인해 주세요.")
			return
		}
		price += int64(value)
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
	_, err = tx.Exec(r.Context(), `INSERT INTO orders(id,order_number,buyer_id,seller_id,talent_id,package_id,state,amount,currency,requirements,due_at,metadata) VALUES($1,$2,$3,$4,$5,$6,'CREATED',$7,$8,$9,$10,$11)`, id, number, p.UserID, sellerID, in.TalentID, in.PackageID, price, currency, in.Requirements, due, map[string]any{"options": in.Options})
	if err == nil {
		_, err = tx.Exec(r.Context(), `INSERT INTO order_timeline(id,order_id,event_type,actor_user_id,to_state,data) VALUES($1,$2,'OrderCreated',$3,'CREATED',$4)`, uuid.New(), id, p.UserID, map[string]any{"amount": price, "currency": currency})
	}
	if err == nil {
		_, err = tx.Exec(r.Context(), `INSERT INTO domain_events(id,aggregate_type,aggregate_id,event_type,payload) VALUES($1,'order',$2,'OrderCreated',$3)`, uuid.New(), id, map[string]any{"buyer_id": p.UserID, "seller_id": sellerID})
	}
	if err != nil {
		writeError(w, 500, "order_failed", "주문을 만들지 못했습니다.")
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		writeError(w, 500, "order_failed", "주문을 만들지 못했습니다.")
		return
	}
	s.audit(r, "order.create", "order", id.String(), nil, map[string]any{"number": number, "amount": price}, "success")
	writeJSON(w, 201, map[string]any{"id": id, "order_number": number, "state": "CREATED", "amount": price, "currency": currency, "due_at": due})
}

func (s *Server) listOrders(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	rows, err := s.DB.Query(r.Context(), `SELECT o.id,o.order_number,o.state,o.amount,o.currency,o.due_at,o.created_at,t.title,o.buyer_id,o.seller_id,bu.display_name,su.display_name FROM orders o JOIN talents t ON t.id=o.talent_id JOIN users bu ON bu.id=o.buyer_id JOIN users su ON su.id=o.seller_id WHERE o.buyer_id=$1 OR o.seller_id=$1 OR $2 ORDER BY o.created_at DESC LIMIT 100`, p.UserID, hasPermission(p, "orders.manage"))
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
	writeJSON(w, 200, map[string]any{"items": items})
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
	var amount int64
	err = tx.QueryRow(r.Context(), `SELECT buyer_id,state,amount,currency FROM orders WHERE id=$1 FOR UPDATE`, id).Scan(&buyer, &state, &amount, &currency)
	if err != nil || buyer != p.UserID && !hasPermission(p, "orders.manage") {
		writeError(w, 403, "payment_denied", "이 주문을 결제할 수 없습니다.")
		return
	}
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
		if err = s.applyOrderTransition(r, tx, id, p.UserID, "CREATED", "PAYMENT_PENDING", map[string]any{"provider": provider}); err != nil {
			writeError(w, 500, "payment_failed", "결제 상태를 저장하지 못했습니다.")
			return
		}
		state = "PAYMENT_PENDING"
	}
	paymentID := uuid.New()
	_, err = tx.Exec(r.Context(), `INSERT INTO payments(id,order_id,provider,provider_reference,state,amount,currency,idempotency_key) VALUES($1,$2,$3,$4,'captured',$5,$6,$7)`, paymentID, id, provider, "manual:"+paymentID.String(), amount, currency, idempotencyKey)
	transactionID := uuid.New()
	if err == nil {
		for _, entry := range []struct{ account, direction string }{{"Buyer Payment", "debit"}, {"Escrow", "credit"}} {
			_, err = tx.Exec(r.Context(), `INSERT INTO ledger_entries(id,transaction_id,order_id,account,direction,amount,currency,description) VALUES($1,$2,$3,$4,$5,$6,$7,'선결제 에스크로 보관')`, uuid.New(), transactionID, id, entry.account, entry.direction, amount, currency)
			if err != nil {
				break
			}
		}
	}
	if err == nil {
		err = s.applyOrderTransition(r, tx, id, p.UserID, state, "PAID", map[string]any{"payment_id": paymentID, "provider": provider})
	}
	if err == nil {
		err = s.applyOrderTransition(r, tx, id, p.UserID, "PAID", "READY", map[string]any{"requirements_collected": true})
	}
	if err != nil {
		writeError(w, 500, "payment_failed", "결제를 완료하지 못했습니다.")
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		writeError(w, 500, "payment_failed", "결제를 완료하지 못했습니다.")
		return
	}
	s.audit(r, "payment.capture", "payment", paymentID.String(), nil, map[string]any{"order_id": id, "amount": amount, "provider": provider}, "success")
	writeJSON(w, 200, map[string]any{"payment_id": paymentID, "state": "READY", "amount": amount, "currency": currency})
}

func (s *Server) getOrder(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	id, ok := parseUUIDPath(w, r, "id")
	if !ok {
		return
	}
	var raw []byte
	err := s.DB.QueryRow(r.Context(), `SELECT jsonb_build_object('id',o.id,'order_number',o.order_number,'state',o.state,'amount',o.amount,'currency',o.currency,'requirements',o.requirements,'due_at',o.due_at,'accepted_at',o.accepted_at,'completed_at',o.completed_at,'created_at',o.created_at,'buyer',jsonb_build_object('id',bu.id,'display_name',bu.display_name),'seller',jsonb_build_object('id',su.id,'display_name',su.display_name),'talent',jsonb_build_object('id',t.id,'title',t.title),'timeline',COALESCE((SELECT jsonb_agg(to_jsonb(tl) ORDER BY tl.created_at) FROM order_timeline tl WHERE tl.order_id=o.id),'[]'::jsonb),'deliveries',COALESCE((SELECT jsonb_agg(to_jsonb(d) ORDER BY d.version) FROM deliveries d WHERE d.order_id=o.id),'[]'::jsonb),'revisions',COALESCE((SELECT jsonb_agg(to_jsonb(rv) ORDER BY rv.revision_number) FROM revisions rv WHERE rv.order_id=o.id),'[]'::jsonb)) FROM orders o JOIN users bu ON bu.id=o.buyer_id JOIN users su ON su.id=o.seller_id JOIN talents t ON t.id=o.talent_id WHERE o.id=$1 AND (o.buyer_id=$2 OR o.seller_id=$2 OR $3)`, id, p.UserID, hasPermission(p, "orders.manage")).Scan(&raw)
	if err != nil {
		writeError(w, 404, "not_found", "주문을 찾을 수 없습니다.")
		return
	}
	var result any
	_ = json.Unmarshal(raw, &result)
	writeJSON(w, 200, result)
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
	err = tx.QueryRow(r.Context(), `SELECT state,buyer_id,seller_id FROM orders WHERE id=$1 FOR UPDATE`, id).Scan(&from, &buyer, &seller)
	if err != nil {
		writeError(w, 404, "not_found", "주문을 찾을 수 없습니다.")
		return
	}
	if !orderTransitions[from][in.To] {
		writeError(w, 409, "invalid_transition", fmt.Sprintf("%s 상태에서 %s 상태로 변경할 수 없습니다.", from, in.To))
		return
	}
	if !canTransition(p, buyer, seller, in.To) {
		writeError(w, 403, "transition_denied", "이 상태 변경을 수행할 권한이 없습니다.")
		return
	}
	if err = s.applyOrderTransition(r, tx, id, p.UserID, from, in.To, map[string]any{"note": in.Note}); err != nil {
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

func (s *Server) applyOrderTransition(r *http.Request, tx pgx.Tx, id, actor uuid.UUID, from, to string, data any) error {
	_, err := tx.Exec(r.Context(), `UPDATE orders SET state=$2,accepted_at=CASE WHEN $2='ACCEPTED' THEN now() ELSE accepted_at END,completed_at=CASE WHEN $2='COMPLETED' THEN now() ELSE completed_at END,updated_at=now() WHERE id=$1`, id, to)
	if err == nil {
		_, err = tx.Exec(r.Context(), `INSERT INTO order_timeline(id,order_id,event_type,actor_user_id,from_state,to_state,data) VALUES($1,$2,$3,$4,$5,$6,$7)`, uuid.New(), id, "Order"+to, actor, from, to, data)
	}
	if err == nil {
		_, err = tx.Exec(r.Context(), `INSERT INTO domain_events(id,aggregate_type,aggregate_id,event_type,payload) VALUES($1,'order',$2,$3,$4)`, uuid.New(), id, "Order"+to, map[string]any{"from": from, "to": to, "actor": actor})
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
		err = s.applyOrderTransition(r, tx, id, p.UserID, state, "DELIVERED", map[string]any{"delivery_id": deliveryID, "version": version})
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
	var number int
	err = tx.QueryRow(r.Context(), `SELECT buyer_id,state,(SELECT count(*)+1 FROM revisions WHERE order_id=$1) FROM orders WHERE id=$1 FOR UPDATE`, id).Scan(&buyer, &state, &number)
	if err != nil || buyer != p.UserID && !hasPermission(p, "orders.manage") {
		writeError(w, 403, "revision_denied", "이 주문에 수정 요청할 권한이 없습니다.")
		return
	}
	if state != "DELIVERED" {
		writeError(w, 409, "invalid_order_state", "납품 완료 상태에서만 수정 요청할 수 있습니다.")
		return
	}
	revisionID := uuid.New()
	_, err = tx.Exec(r.Context(), `INSERT INTO revisions(id,order_id,delivery_id,revision_number,details,priority,attachments) VALUES($1,$2,$3,$4,$5,$6,$7)`, revisionID, id, in.DeliveryID, number, in.Details, in.Priority, in.Attachments)
	if err == nil {
		err = s.applyOrderTransition(r, tx, id, p.UserID, state, "REVISION_REQUESTED", map[string]any{"revision_id": revisionID, "revision_number": number})
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
	writeJSON(w, 201, map[string]any{"id": revisionID, "revision_number": number, "order_state": "REVISION_REQUESTED"})
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
	feeRate := float64(10)
	if setting, err := s.settingObject(r, "marketplace.policy"); err == nil {
		if value, ok := setting["platform_fee_rate"].(float64); ok && value >= 0 && value <= 100 {
			feeRate = value
		}
	}
	fee := int64(float64(amount) * feeRate / 100)
	net := amount - fee
	if err = s.applyOrderTransition(r, tx, id, p.UserID, state, "ACCEPTED", map[string]any{"platform_fee": fee}); err != nil {
		writeError(w, 500, "accept_failed", "구매확정을 저장하지 못했습니다.")
		return
	}
	settlementID := uuid.New()
	_, err = tx.Exec(r.Context(), `INSERT INTO settlements(id,order_id,seller_id,gross_amount,platform_fee,net_amount,scheduled_at) VALUES($1,$2,$3,$4,$5,$6,now()+interval '3 days')`, settlementID, id, seller, amount, fee, net)
	transactionID := uuid.New()
	entries := []struct {
		Account, Direction string
		Amount             int64
	}{{"Escrow", "debit", amount}, {"Platform Revenue", "credit", fee}, {"Seller Payable", "credit", net}}
	if err == nil {
		for _, entry := range entries {
			_, err = tx.Exec(r.Context(), `INSERT INTO ledger_entries(id,transaction_id,order_id,account,direction,amount,currency,description) VALUES($1,$2,$3,$4,$5,$6,$7,'구매확정 정산 예정')`, uuid.New(), transactionID, id, entry.Account, entry.Direction, entry.Amount, currency)
			if err != nil {
				break
			}
		}
	}
	if err == nil {
		_, err = tx.Exec(r.Context(), `INSERT INTO domain_events(id,aggregate_type,aggregate_id,event_type,payload) VALUES($1,'settlement',$2,'SettlementCreated',$3)`, uuid.New(), settlementID, map[string]any{"order_id": id, "net_amount": net})
	}
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
