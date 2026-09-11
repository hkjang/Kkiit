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

func (s *Server) createRFQ(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	var in struct {
		Title        string         `json:"title"`
		Description  string         `json:"description"`
		Requirements map[string]any `json:"requirements"`
		BudgetMin    *int64         `json:"budget_min,omitempty"`
		BudgetMax    *int64         `json:"budget_max,omitempty"`
		Currency     string         `json:"currency"`
		DesiredDueAt *time.Time     `json:"desired_due_at,omitempty"`
		ExpiresAt    *time.Time     `json:"expires_at,omitempty"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	in.Title = strings.TrimSpace(in.Title)
	in.Description = strings.TrimSpace(in.Description)
	if len([]rune(in.Title)) < 3 || len([]rune(in.Title)) > 160 || len([]rune(in.Description)) < 10 {
		writeError(w, 400, "invalid_rfq", "프로젝트 제목과 상세 요구사항을 확인해 주세요.")
		return
	}
	if in.Currency == "" {
		in.Currency = "KRW"
	}
	if in.BudgetMin != nil && *in.BudgetMin < 0 || in.BudgetMax != nil && *in.BudgetMax < 0 || in.BudgetMin != nil && in.BudgetMax != nil && *in.BudgetMin > *in.BudgetMax {
		writeError(w, 400, "invalid_budget", "예산 범위를 확인해 주세요.")
		return
	}
	if in.Requirements == nil {
		in.Requirements = map[string]any{}
	}
	id := uuid.New()
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		writeError(w, 500, "transaction_failed", "견적 요청을 시작하지 못했습니다.")
		return
	}
	defer tx.Rollback(r.Context()) //nolint:errcheck
	_, err = tx.Exec(r.Context(), `INSERT INTO rfqs(id,buyer_id,title,description,requirements,budget_min,budget_max,currency,desired_due_at,state,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,'open',$10)`, id, p.UserID, in.Title, in.Description, in.Requirements, in.BudgetMin, in.BudgetMax, in.Currency, in.DesiredDueAt, in.ExpiresAt)
	if err == nil {
		err = emitEvent(r.Context(), tx, "rfq", id, "RFQCreated", map[string]any{"rfq_title": in.Title, "budget_max": in.BudgetMax, "currency": in.Currency, "actor": p.UserID})
	}
	if err == nil {
		err = tx.Commit(r.Context())
	}
	if err != nil {
		writeError(w, 500, "rfq_failed", "견적 요청을 만들지 못했습니다.")
		return
	}
	s.audit(r, "rfq.create", "rfq", id.String(), nil, map[string]any{"title": in.Title}, "success")
	writeJSON(w, 201, map[string]any{"id": id, "state": "open"})
}

// listRFQs carried the same two faults as the order list: a flat ceiling that
// put older requests out of reach, and role bypasses expressed as query
// parameters, which a prepared statement's generic plan cannot fold away.
func (s *Server) listRFQs(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	limit := queryLimit(r, 50, 200)
	cursorAt, cursorID := requestCursor(r)
	visible := `buyer_id=$1`
	switch {
	case hasPermission(p, "orders.manage"):
		visible = `$1::uuid IS NOT NULL`
	case hasPermission(p, "orders.sell"):
		visible = `(buyer_id=$1 OR state='open')`
	}
	rows, err := s.DB.Query(r.Context(), `SELECT id,buyer_id,title,description,requirements,budget_min,budget_max,currency,desired_due_at,state,expires_at,created_at
		FROM rfqs WHERE `+visible+` AND ($3::timestamptz IS NULL OR (created_at,id) < ($3,$4))
		ORDER BY created_at DESC,id DESC LIMIT $2`, p.UserID, limit+1, cursorAt, cursorID)
	if err != nil {
		writeError(w, 500, "query_failed", "견적 요청을 조회하지 못했습니다.")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id, buyer uuid.UUID
		var title, description, currency, state string
		var requirements []byte
		var min, max *int64
		var due, expires *time.Time
		var created time.Time
		if rows.Scan(&id, &buyer, &title, &description, &requirements, &min, &max, &currency, &due, &state, &expires, &created) == nil {
			var req any
			_ = json.Unmarshal(requirements, &req)
			items = append(items, map[string]any{"id": id, "buyer_id": buyer, "title": title, "description": description, "requirements": req, "budget_min": min, "budget_max": max, "currency": currency, "desired_due_at": due, "state": state, "expires_at": expires, "created_at": created})
		}
	}
	items, next := pageResult(items, limit, func(item map[string]any) (time.Time, uuid.UUID) {
		return timeField(item, "created_at"), uuidField(item, "id")
	})
	writeJSON(w, 200, map[string]any{"items": items, "next_cursor": next})
}

func (s *Server) createQuote(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	var in struct {
		RFQID        uuid.UUID        `json:"rfq_id"`
		Amount       int64            `json:"amount"`
		Currency     string           `json:"currency"`
		DeliveryDays int              `json:"delivery_days"`
		Scope        map[string]any   `json:"scope"`
		Milestones   []map[string]any `json:"milestones"`
		TalentID     *uuid.UUID       `json:"talent_id,omitempty"`
		ExpiresAt    *time.Time       `json:"expires_at,omitempty"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.Amount < 0 || in.DeliveryDays < 1 {
		writeError(w, 400, "invalid_quote", "견적 금액과 작업 기간을 확인해 주세요.")
		return
	}
	if in.Currency == "" {
		in.Currency = "KRW"
	}
	if in.Scope == nil {
		in.Scope = map[string]any{}
	}
	if in.Milestones == nil {
		in.Milestones = []map[string]any{}
	}
	var buyer uuid.UUID
	var state string
	var budgetMax *int64
	if err := s.DB.QueryRow(r.Context(), `SELECT buyer_id,state,budget_max FROM rfqs WHERE id=$1`, in.RFQID).Scan(&buyer, &state, &budgetMax); err != nil || state != "open" {
		writeError(w, 404, "rfq_not_open", "견적 가능한 요청을 찾을 수 없습니다.")
		return
	}
	if buyer == p.UserID {
		writeError(w, 400, "self_quote_not_allowed", "자신의 요청에는 견적할 수 없습니다.")
		return
	}
	score := float64(80)
	if budgetMax != nil && *budgetMax > 0 {
		if in.Amount <= *budgetMax {
			score += 10
		} else {
			over := float64(in.Amount-*budgetMax) / float64(*budgetMax)
			score -= over * 30
		}
	}
	if score < 0 {
		score = 0
	}
	if score > 100 {
		score = 100
	}
	id := uuid.New()
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		writeError(w, 500, "transaction_failed", "견적 제출을 시작하지 못했습니다.")
		return
	}
	defer tx.Rollback(r.Context()) //nolint:errcheck
	if in.TalentID != nil {
		var owner uuid.UUID
		if tx.QueryRow(r.Context(), `SELECT seller_id FROM talents WHERE id=$1 AND status='published'`, *in.TalentID).Scan(&owner) != nil || owner != p.UserID {
			writeError(w, 400, "invalid_talent", "본인이 공개한 상품만 견적에 연결할 수 있습니다.")
			return
		}
	}
	err = tx.QueryRow(r.Context(), `INSERT INTO quotes(id,rfq_id,seller_id,amount,currency,delivery_days,scope,milestones,match_score,expires_at,talent_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) ON CONFLICT(rfq_id,seller_id) DO UPDATE SET amount=EXCLUDED.amount,currency=EXCLUDED.currency,delivery_days=EXCLUDED.delivery_days,scope=EXCLUDED.scope,milestones=EXCLUDED.milestones,match_score=EXCLUDED.match_score,expires_at=EXCLUDED.expires_at,talent_id=EXCLUDED.talent_id,updated_at=now() WHERE quotes.state='submitted' RETURNING id`, id, in.RFQID, p.UserID, in.Amount, in.Currency, in.DeliveryDays, in.Scope, in.Milestones, score, in.ExpiresAt, in.TalentID).Scan(&id)
	if err == nil {
		err = emitEvent(r.Context(), tx, "quote", id, "QuoteCreated", map[string]any{"rfq_id": in.RFQID, "amount": in.Amount, "currency": in.Currency, "delivery_days": in.DeliveryDays, "match_score": score, "actor": p.UserID})
	}
	if err == nil {
		err = tx.Commit(r.Context())
	}
	if err != nil {
		writeError(w, 500, "quote_failed", "견적을 저장하지 못했습니다.")
		return
	}
	s.audit(r, "quote.submit", "quote", id.String(), nil, map[string]any{"rfq_id": in.RFQID, "amount": in.Amount}, "success")
	writeJSON(w, 201, map[string]any{"id": id, "match_score": score})
}

func (s *Server) listQuotes(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	rfqID, ok := parseUUIDPath(w, r, "id")
	if !ok {
		return
	}
	var buyer uuid.UUID
	if err := s.DB.QueryRow(r.Context(), `SELECT buyer_id FROM rfqs WHERE id=$1`, rfqID).Scan(&buyer); err != nil {
		writeError(w, 404, "rfq_not_found", "견적 요청을 찾을 수 없습니다.")
		return
	}
	rows, err := s.DB.Query(r.Context(), `SELECT q.id,q.seller_id,u.display_name,q.amount,q.currency,q.delivery_days,q.scope,q.milestones,q.match_score,q.state,q.expires_at,q.created_at,q.talent_id,COALESCE(t.title,''),q.order_id FROM quotes q JOIN users u ON u.id=q.seller_id LEFT JOIN talents t ON t.id=q.talent_id WHERE q.rfq_id=$1 AND ($2 OR q.seller_id=$3) ORDER BY q.match_score DESC NULLS LAST,q.amount,q.delivery_days`, rfqID, p.UserID == buyer || hasPermission(p, "orders.manage"), p.UserID)
	if err != nil {
		writeError(w, 500, "query_failed", "견적을 조회하지 못했습니다.")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id, seller uuid.UUID
		var name, currency, state string
		var amount int64
		var days int
		var scope, milestones []byte
		var score *float64
		var expires *time.Time
		var created time.Time
		var talentID, orderID *uuid.UUID
		var talentTitle string
		if rows.Scan(&id, &seller, &name, &amount, &currency, &days, &scope, &milestones, &score, &state, &expires, &created, &talentID, &talentTitle, &orderID) == nil {
			var sc, ms any
			_ = json.Unmarshal(scope, &sc)
			_ = json.Unmarshal(milestones, &ms)
			items = append(items, map[string]any{"id": id, "seller": map[string]any{"id": seller, "display_name": name}, "amount": amount, "currency": currency, "delivery_days": days, "scope": sc, "milestones": ms, "match_score": score, "state": state, "expires_at": expires, "created_at": created, "talent_id": talentID, "talent_title": talentTitle, "order_id": orderID})
		}
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

// matchingWeights is the operator tuned blend from matching.weights. Only the
// factors the platform can actually measure are used; the rest stay at zero so
// the reported score never claims more than the data supports.
type matchingWeights struct {
	Requirement, Skill, Price, Seller, Repeat float64
}

func (s *Server) matchingWeights(r *http.Request) matchingWeights {
	raw, err := s.settingObject(r, "matching.weights")
	if err != nil {
		raw = map[string]any{}
	}
	value := func(key string, fallback float64) float64 {
		if number, ok := raw[key].(float64); ok && number >= 0 && number <= 100 {
			return number
		}
		return fallback
	}
	weights := matchingWeights{
		Requirement: value("requirement", 30),
		Skill:       value("skill", 20),
		Price:       value("price", 10),
		Seller:      value("rating", 10) + value("on_time", 10) + value("response", 5) + value("completion", 10),
		Repeat:      value("repeat", 5),
	}
	if weights.Requirement+weights.Skill+weights.Price+weights.Seller+weights.Repeat == 0 {
		return matchingWeights{Requirement: 30, Skill: 20, Price: 10, Seller: 35, Repeat: 5}
	}
	return weights
}

func (s *Server) recommendTalents(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	budgetMax := int64(0)
	if raw := r.URL.Query().Get("budget_max"); raw != "" {
		budgetMax, _ = strconv.ParseInt(raw, 10, 64)
	}
	weights := s.matchingWeights(r)
	// Same reason as the marketplace search: the text predicate is only added
	// when there is text, so the index behind it stays reachable.
	match := ""
	if query != "" {
		match = ` AND (t.search_document@@websearch_to_tsquery('simple',$1) OR t.title ILIKE '%'||$1||'%')`
	}
	rows, err := s.DB.Query(r.Context(), `WITH scored AS (
			SELECT t.id,t.title,t.summary,t.base_price,t.currency,t.delivery_days,u.display_name,
				COALESCE(sp.score,0) AS seller_score,COALESCE(sp.rating,0) AS seller_rating,COALESCE(sp.level,'NEW') AS seller_level,
				COALESCE(t.quality_score,50) AS quality,
				CASE WHEN $1='' THEN 0.5 ELSE LEAST(1,ts_rank(t.search_document,websearch_to_tsquery('simple',$1))*10) END AS text_fit,
				CASE WHEN $1='' THEN 0.5 WHEN t.tags && regexp_split_to_array(lower($1),'\s+') THEN 1 ELSE 0 END AS skill_fit,
				CASE WHEN $2=0 THEN 0.6 WHEN t.base_price<=$2 THEN 1 ELSE GREATEST(0,1-(t.base_price-$2)::numeric/NULLIF($2,0)) END AS price_fit,
				CASE WHEN $3::uuid IS NOT NULL AND EXISTS(SELECT 1 FROM orders o WHERE o.buyer_id=$3 AND o.seller_id=t.seller_id AND o.state='COMPLETED') THEN 1 ELSE 0 END AS repeat_fit
			FROM talents t JOIN users u ON u.id=t.seller_id LEFT JOIN seller_profiles sp ON sp.user_id=t.seller_id
			WHERE t.status='published' AND u.status='active'`+match+`)
		SELECT id,title,summary,base_price,currency,delivery_days,display_name,seller_score,seller_rating,seller_level,quality,
			text_fit,skill_fit,price_fit,repeat_fit,
			LEAST(100,GREATEST(0,$4*text_fit+$5*skill_fit+$6*price_fit+$7*quality/100+$8*repeat_fit)) AS match_score
		FROM scored ORDER BY match_score DESC,quality DESC LIMIT 10`,
		query, budgetMax, principalID(r), weights.Requirement, weights.Skill, weights.Price, weights.Seller, weights.Repeat)
	if err != nil {
		writeError(w, 500, "query_failed", "추천을 계산하지 못했습니다.")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id uuid.UUID
		var title, summary, currency, seller, level string
		var price int64
		var days int
		var sellerScore, sellerRating, quality, textFit, skillFit, priceFit, repeatFit, match float64
		if rows.Scan(&id, &title, &summary, &price, &currency, &days, &seller, &sellerScore, &sellerRating, &level, &quality,
			&textFit, &skillFit, &priceFit, &repeatFit, &match) != nil {
			continue
		}
		components := map[string]float64{"requirement": round2(weights.Requirement * textFit), "skill": round2(weights.Skill * skillFit),
			"price": round2(weights.Price * priceFit), "seller": round2(weights.Seller * quality / 100), "repeat": round2(weights.Repeat * repeatFit)}
		items = append(items, map[string]any{"id": id, "title": title, "summary": summary, "base_price": price, "currency": currency,
			"delivery_days": days, "seller_name": seller, "seller_score": sellerScore, "seller_rating": sellerRating, "seller_level": level,
			"quality_score": quality, "match_score": round2(match), "components": components,
			"explanation": explainMatch(components, budgetMax > 0 && price <= budgetMax, repeatFit > 0)})
	}
	writeJSON(w, 200, map[string]any{"items": items, "algorithm_version": "ranking_v2"})
}

// explainMatch names the factor that contributed most so a buyer can see why a
// product was suggested instead of trusting a bare number.
func explainMatch(components map[string]float64, withinBudget, repeatSeller bool) string {
	labels := map[string]string{"requirement": "요구사항 적합도", "skill": "기술 태그 일치", "price": "예산 적합", "seller": "판매자 신뢰도", "repeat": "재구매 이력"}
	top, best := "requirement", -1.0
	for key, value := range components {
		if value > best {
			top, best = key, value
		}
	}
	reason := labels[top] + "이(가) 가장 높은 상품입니다."
	if repeatSeller {
		reason = "이전에 거래한 판매자이며 " + reason
	} else if withinBudget {
		reason = "예산 범위 안이며 " + reason
	}
	return reason
}

func round2(value float64) float64 {
	return float64(int64(value*100+0.5)) / 100
}

// acceptQuote turns a chosen quote into a real order. Without it the whole
// request-for-quote flow dead ended: buyers could collect quotes and then had
// no way to act on one.
func (s *Server) acceptQuote(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	quoteID, ok := parseUUIDPath(w, r, "id")
	if !ok {
		return
	}
	var in struct {
		CouponCode     string         `json:"coupon_code,omitempty"`
		OrganizationID *uuid.UUID     `json:"organization_id,omitempty"`
		Requirements   map[string]any `json:"requirements"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.Requirements == nil {
		in.Requirements = map[string]any{}
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		writeError(w, 500, "transaction_failed", "견적 선택을 시작하지 못했습니다.")
		return
	}
	defer tx.Rollback(r.Context()) //nolint:errcheck
	var rfqID, seller, buyer uuid.UUID
	var talentID *uuid.UUID
	var quoteState, rfqState, currency, rfqTitle string
	var amount int64
	var days int
	err = tx.QueryRow(r.Context(), `SELECT q.rfq_id,q.seller_id,q.talent_id,q.state,q.amount,q.currency,q.delivery_days,r.buyer_id,r.state,r.title
		FROM quotes q JOIN rfqs r ON r.id=q.rfq_id WHERE q.id=$1 FOR UPDATE OF q`, quoteID).
		Scan(&rfqID, &seller, &talentID, &quoteState, &amount, &currency, &days, &buyer, &rfqState, &rfqTitle)
	if err != nil {
		writeError(w, 404, "quote_not_found", "견적을 찾을 수 없습니다.")
		return
	}
	if buyer != p.UserID {
		writeError(w, 403, "quote_accept_denied", "이 견적을 선택할 권한이 없습니다.")
		return
	}
	if quoteState != "submitted" || rfqState != "open" {
		writeError(w, 409, "quote_not_selectable", "이미 처리된 견적 요청입니다.")
		return
	}
	if talentID == nil {
		writeError(w, 409, "quote_talent_required", "판매자가 견적에 상품을 연결하지 않아 주문을 만들 수 없습니다. 판매자에게 상품 연결을 요청해 주세요.")
		return
	}
	// Inside a transaction the capacity check has to be asked of the same
	// transaction, or it reads a snapshot the order is not written against.
	if full, capacity, err := s.sellerAtCapacityTx(r.Context(), tx, seller); err != nil {
		writeError(w, 500, "capacity_check_failed", "판매자의 진행 상황을 확인하지 못했습니다.")
		return
	} else if full {
		writeError(w, 409, "seller_at_capacity", fmt.Sprintf("이 판매자는 동시에 진행할 수 있는 주문 %d건을 모두 소화하고 있습니다.", capacity))
		return
	}
	var sellerActive bool
	if tx.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM talents t JOIN users u ON u.id=t.seller_id WHERE t.id=$1 AND t.status='published' AND u.status='active')`, *talentID).Scan(&sellerActive) != nil || !sellerActive {
		writeError(w, 409, "quote_talent_unavailable", "견적에 연결된 상품을 지금 주문할 수 없습니다.")
		return
	}
	// The brief for this order is the request for quotation the seller already
	// read and priced. Anything the buyer adds through the listing's own form is
	// kept alongside it, but the form is not demanded again.
	requirements, message, ok := normalizeRequirements(r.Context(), tx, *talentID, in.Requirements, false)
	if !ok {
		writeError(w, 400, "requirements_incomplete", message)
		return
	}
	var rfqDescription string
	if tx.QueryRow(r.Context(), `SELECT description FROM rfqs WHERE id=$1`, rfqID).Scan(&rfqDescription) == nil && strings.TrimSpace(rfqDescription) != "" {
		requirements["견적 요청 내용"] = strings.TrimSpace(rfqDescription)
	}
	coupon, couponMessage, ok := resolveCoupon(r.Context(), tx, in.CouponCode, p.UserID, amount)
	if !ok {
		writeError(w, 409, "coupon_not_applicable", couponMessage)
		return
	}
	var organizationID, budgetID any
	var reserved int64
	if in.OrganizationID != nil {
		if memberRole(r.Context(), tx, *in.OrganizationID, p.UserID) == "" {
			writeError(w, 403, "organization_membership_required", "이 조직으로 주문할 권한이 없습니다.")
			return
		}
		if !s.featureEnabled(r.Context(), "enterprise") {
			writeError(w, 404, "feature_disabled", "조직 계정 기능이 비활성화되어 있습니다.")
			return
		}
		organizationID = *in.OrganizationID
		var budgetMessage string
		var reserveOK bool
		var exhausted uuid.UUID
		budgetID, reserved, budgetMessage, reserveOK, exhausted = reserveBudget(r.Context(), tx, *in.OrganizationID, amount-coupon.Discount)
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
	var couponID any
	if coupon.ID != uuid.Nil {
		couponID = coupon.ID
	}
	orderID := uuid.New()
	number := fmt.Sprintf("KK-%s-%s", time.Now().Format("20060102"), strings.ToUpper(strings.ReplaceAll(uuid.NewString()[:8], "-", "")))
	due := time.Now().AddDate(0, 0, days)
	_, err = tx.Exec(r.Context(), `INSERT INTO orders(id,order_number,buyer_id,seller_id,talent_id,state,amount,currency,requirements,due_at,metadata,coupon_id,discount_amount,organization_id,budget_id,budget_consumed)
		VALUES($1,$2,$3,$4,$5,'CREATED',$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`,
		orderID, number, p.UserID, seller, *talentID, amount, currency, requirements, due,
		map[string]any{"quote_id": quoteID, "rfq_id": rfqID}, couponID, coupon.Discount, organizationID, budgetID, reserved)
	if err == nil && coupon.ID != uuid.Nil {
		_, err = tx.Exec(r.Context(), `INSERT INTO coupon_redemptions(id,coupon_id,user_id,order_id,discount_amount) VALUES($1,$2,$3,$4,$5)`, uuid.New(), coupon.ID, p.UserID, orderID, coupon.Discount)
	}
	if err == nil {
		_, err = tx.Exec(r.Context(), `INSERT INTO order_timeline(id,order_id,event_type,actor_user_id,to_state,data) VALUES($1,$2,'OrderCreated',$3,'CREATED',$4)`,
			uuid.New(), orderID, p.UserID, map[string]any{"amount": amount, "currency": currency, "quote_id": quoteID})
	}
	// The losing quotes close with the request so no seller is left waiting.
	if err == nil {
		_, err = tx.Exec(r.Context(), `UPDATE quotes SET state=CASE WHEN id=$2 THEN 'accepted' ELSE 'rejected' END,order_id=CASE WHEN id=$2 THEN $3 ELSE order_id END,updated_at=now() WHERE rfq_id=$1 AND state='submitted'`, rfqID, quoteID, orderID)
	}
	if err == nil {
		_, err = tx.Exec(r.Context(), `UPDATE rfqs SET state='awarded',updated_at=now() WHERE id=$1`, rfqID)
	}
	// Sellers who lost should hear it rather than waiting on a quote that can
	// never be chosen.
	if err == nil {
		var rejected []uuid.UUID
		rejected, err = rejectedQuoteIDs(r.Context(), tx, rfqID, quoteID)
		for _, loser := range rejected {
			if err != nil {
				break
			}
			err = emitEvent(r.Context(), tx, "quote", loser, "QuoteRejected", map[string]any{"rfq_id": rfqID, "rfq_title": rfqTitle, "actor": p.UserID})
		}
	}
	if err == nil {
		err = emitEvent(r.Context(), tx, "order", orderID, "OrderCreated", map[string]any{"buyer_id": p.UserID, "seller_id": seller, "actor": p.UserID, "quote_id": quoteID})
	}
	if err == nil {
		err = emitEvent(r.Context(), tx, "quote", quoteID, "QuoteAccepted", map[string]any{"rfq_id": rfqID, "rfq_title": rfqTitle, "order_id": orderID, "order_number": number, "actor": p.UserID})
	}
	if err != nil {
		writeError(w, 500, "quote_accept_failed", "견적을 주문으로 전환하지 못했습니다.")
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		writeError(w, 500, "quote_accept_failed", "견적을 주문으로 전환하지 못했습니다.")
		return
	}
	s.audit(r, "quote.accept", "quote", quoteID.String(), nil, map[string]any{"order_id": orderID, "amount": amount, "rfq_id": rfqID}, "success")
	writeJSON(w, 201, map[string]any{"order_id": orderID, "order_number": number, "amount": amount,
		"discount_amount": coupon.Discount, "payable_amount": amount - coupon.Discount, "currency": currency, "due_at": due})
}

func rejectedQuoteIDs(ctx context.Context, tx pgx.Tx, rfqID, winner uuid.UUID) ([]uuid.UUID, error) {
	rows, err := tx.Query(ctx, `SELECT id FROM quotes WHERE rfq_id=$1 AND id<>$2 AND state='rejected'`, rfqID, winner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]uuid.UUID, 0)
	for rows.Next() {
		var id uuid.UUID
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	return ids, rows.Err()
}
