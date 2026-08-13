package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
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
	_, err := s.DB.Exec(r.Context(), `INSERT INTO rfqs(id,buyer_id,title,description,requirements,budget_min,budget_max,currency,desired_due_at,state,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,'open',$10)`, id, p.UserID, in.Title, in.Description, in.Requirements, in.BudgetMin, in.BudgetMax, in.Currency, in.DesiredDueAt, in.ExpiresAt)
	if err != nil {
		writeError(w, 500, "rfq_failed", "견적 요청을 만들지 못했습니다.")
		return
	}
	s.audit(r, "rfq.create", "rfq", id.String(), nil, map[string]any{"title": in.Title}, "success")
	writeJSON(w, 201, map[string]any{"id": id, "state": "open"})
}

func (s *Server) listRFQs(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	seller := hasPermission(p, "orders.sell")
	rows, err := s.DB.Query(r.Context(), `SELECT id,buyer_id,title,description,requirements,budget_min,budget_max,currency,desired_due_at,state,expires_at,created_at FROM rfqs WHERE buyer_id=$1 OR ($2 AND state='open') OR $3 ORDER BY created_at DESC LIMIT 100`, p.UserID, seller, hasPermission(p, "orders.manage"))
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
	writeJSON(w, 200, map[string]any{"items": items})
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
	err := s.DB.QueryRow(r.Context(), `INSERT INTO quotes(id,rfq_id,seller_id,amount,currency,delivery_days,scope,milestones,match_score,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) ON CONFLICT(rfq_id,seller_id) DO UPDATE SET amount=EXCLUDED.amount,currency=EXCLUDED.currency,delivery_days=EXCLUDED.delivery_days,scope=EXCLUDED.scope,milestones=EXCLUDED.milestones,match_score=EXCLUDED.match_score,expires_at=EXCLUDED.expires_at,updated_at=now() RETURNING id`, id, in.RFQID, p.UserID, in.Amount, in.Currency, in.DeliveryDays, in.Scope, in.Milestones, score, in.ExpiresAt).Scan(&id)
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
	rows, err := s.DB.Query(r.Context(), `SELECT q.id,q.seller_id,u.display_name,q.amount,q.currency,q.delivery_days,q.scope,q.milestones,q.match_score,q.state,q.expires_at,q.created_at FROM quotes q JOIN users u ON u.id=q.seller_id WHERE q.rfq_id=$1 AND ($2 OR q.seller_id=$3) ORDER BY q.match_score DESC NULLS LAST,q.amount,q.delivery_days`, rfqID, p.UserID == buyer || hasPermission(p, "orders.manage"), p.UserID)
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
		if rows.Scan(&id, &seller, &name, &amount, &currency, &days, &scope, &milestones, &score, &state, &expires, &created) == nil {
			var sc, ms any
			_ = json.Unmarshal(scope, &sc)
			_ = json.Unmarshal(milestones, &ms)
			items = append(items, map[string]any{"id": id, "seller": map[string]any{"id": seller, "display_name": name}, "amount": amount, "currency": currency, "delivery_days": days, "scope": sc, "milestones": ms, "match_score": score, "state": state, "expires_at": expires, "created_at": created})
		}
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

func (s *Server) recommendTalents(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	budgetMax := int64(0)
	if raw := r.URL.Query().Get("budget_max"); raw != "" {
		budgetMax, _ = strconv.ParseInt(raw, 10, 64)
	}
	rows, err := s.DB.Query(r.Context(), `SELECT t.id,t.title,t.summary,t.base_price,t.currency,t.delivery_days,u.display_name,COALESCE(sp.score,0),LEAST(100,GREATEST(0,(CASE WHEN $1='' THEN 40 ELSE ts_rank(t.search_document,websearch_to_tsquery('simple',$1))*50+40 END)+(CASE WHEN $2=0 OR t.base_price<=$2 THEN 15 ELSE -15 END)+COALESCE(sp.score,0)*5)) AS match_score FROM talents t JOIN users u ON u.id=t.seller_id LEFT JOIN seller_profiles sp ON sp.user_id=t.seller_id WHERE t.status='published' AND ($1='' OR t.search_document@@websearch_to_tsquery('simple',$1) OR t.title ILIKE '%'||$1||'%') ORDER BY match_score DESC,t.published_at DESC LIMIT 10`, query, budgetMax)
	if err != nil {
		writeError(w, 500, "query_failed", "추천을 계산하지 못했습니다.")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id uuid.UUID
		var title, summary, currency, seller string
		var price int64
		var days int
		var sellerScore, match float64
		if rows.Scan(&id, &title, &summary, &price, &currency, &days, &seller, &sellerScore, &match) == nil {
			reason := "요구사항과 공개 상품의 검색 적합도를 기준으로 추천했습니다."
			if budgetMax > 0 && price <= budgetMax {
				reason = "예산 범위 안이며 요구사항과 관련성이 높은 상품입니다."
			}
			items = append(items, map[string]any{"id": id, "title": title, "summary": summary, "base_price": price, "currency": currency, "delivery_days": days, "seller_name": seller, "seller_score": sellerScore, "match_score": match, "explanation": reason})
		}
	}
	writeJSON(w, 200, map[string]any{"items": items, "algorithm_version": "ranking_v1"})
}
