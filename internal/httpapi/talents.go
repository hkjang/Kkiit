package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type talentInput struct {
	CategoryID          *uuid.UUID         `json:"category_id,omitempty"`
	Title               string             `json:"title"`
	Slug                string             `json:"slug"`
	Summary             string             `json:"summary"`
	Description         string             `json:"description"`
	ServiceType         string             `json:"service_type"`
	BasePrice           int64              `json:"base_price"`
	Currency            string             `json:"currency"`
	DeliveryDays        int                `json:"delivery_days"`
	RevisionCount       int                `json:"revision_count"`
	ScopeIncluded       any                `json:"scope_included"`
	ScopeExcluded       any                `json:"scope_excluded"`
	Deliverables        any                `json:"deliverables"`
	Tags                []string           `json:"tags"`
	FAQ                 any                `json:"faq"`
	RefundPolicy        string             `json:"refund_policy"`
	InstantOrder        bool               `json:"instant_order"`
	QuoteRequired       bool               `json:"quote_required"`
	SubscriptionEnabled bool               `json:"subscription_enabled"`
	Packages            []packageInput     `json:"packages"`
	Requirements        []requirementInput `json:"requirements"`
}
type packageInput struct {
	ID            *uuid.UUID `json:"id,omitempty"`
	Type          string     `json:"package_type"`
	Name          string     `json:"name"`
	Description   string     `json:"description"`
	Price         int64      `json:"price"`
	DeliveryDays  int        `json:"delivery_days"`
	RevisionCount int        `json:"revision_count"`
	Features      any        `json:"features"`
	Deliverables  any        `json:"deliverables"`
	SortOrder     int        `json:"sort_order"`
	Active        bool       `json:"active"`
}
type requirementInput struct {
	ID         *uuid.UUID `json:"id,omitempty"`
	Label      string     `json:"label"`
	HelpText   string     `json:"help_text"`
	FieldType  string     `json:"field_type"`
	Required   bool       `json:"required"`
	Options    any        `json:"options"`
	Validation any        `json:"validation"`
	SortOrder  int        `json:"sort_order"`
}

func (s *Server) listCategories(w http.ResponseWriter, r *http.Request) {
	rows, err := s.DB.Query(r.Context(), `SELECT id,parent_id,slug,name,description,sort_order FROM categories WHERE active ORDER BY sort_order,name`)
	if err != nil {
		writeError(w, 500, "query_failed", "카테고리를 조회하지 못했습니다.")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id uuid.UUID
		var parent *uuid.UUID
		var slug, name, description string
		var sort int
		if rows.Scan(&id, &parent, &slug, &name, &description, &sort) == nil {
			items = append(items, map[string]any{"id": id, "parent_id": parent, "slug": slug, "name": name, "description": description, "sort_order": sort})
		}
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

func (s *Server) listMyTalents(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	rows, err := s.DB.Query(r.Context(), `SELECT id,title,slug,status,service_type,base_price,currency,delivery_days,updated_at FROM talents WHERE seller_id=$1 ORDER BY updated_at DESC`, p.UserID)
	if err != nil {
		writeError(w, 500, "query_failed", "내 상품을 조회하지 못했습니다.")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id uuid.UUID
		var title, slug, status, service, currency string
		var price int64
		var days int
		var updated time.Time
		if rows.Scan(&id, &title, &slug, &status, &service, &price, &currency, &days, &updated) == nil {
			items = append(items, map[string]any{"id": id, "title": title, "slug": slug, "status": status, "service_type": service, "base_price": price, "currency": currency, "delivery_days": days, "updated_at": updated})
		}
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

func (s *Server) listTalents(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	limit := 24
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if parsed, e := strconv.Atoi(raw); e == nil && parsed > 0 && parsed <= 100 {
			limit = parsed
		}
	}
	rows, err := s.DB.Query(r.Context(), `SELECT t.id,t.title,t.slug,t.summary,t.service_type,t.base_price,t.currency,t.delivery_days,t.tags,t.quality_score,t.published_at,u.id,u.display_name,COALESCE(sp.level,'NEW'),COALESCE(sp.score,0) FROM talents t JOIN users u ON u.id=t.seller_id LEFT JOIN seller_profiles sp ON sp.user_id=u.id WHERE t.status='published' AND ($1='' OR t.search_document @@ websearch_to_tsquery('simple',$1) OR t.title ILIKE '%'||$1||'%') ORDER BY CASE WHEN $1='' THEN 0 ELSE ts_rank(t.search_document,websearch_to_tsquery('simple',$1)) END DESC,t.published_at DESC LIMIT $2`, query, limit)
	if err != nil {
		writeError(w, 500, "query_failed", "재능 상품을 검색하지 못했습니다.")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id, sellerID uuid.UUID
		var title, slug, summary, service, currency, display, level string
		var price int64
		var days int
		var tags []string
		var quality *float64
		var published *time.Time
		var score float64
		if rows.Scan(&id, &title, &slug, &summary, &service, &price, &currency, &days, &tags, &quality, &published, &sellerID, &display, &level, &score) == nil {
			items = append(items, map[string]any{"id": id, "title": title, "slug": slug, "summary": summary, "service_type": service, "base_price": price, "currency": currency, "delivery_days": days, "tags": tags, "quality_score": quality, "published_at": published, "seller": map[string]any{"id": sellerID, "display_name": display, "level": level, "score": score}})
		}
	}
	writeJSON(w, 200, map[string]any{"items": items, "query": query})
}

func validateTalent(in *talentInput) bool {
	in.Title = strings.TrimSpace(in.Title)
	if len(in.Title) < 3 || len(in.Title) > 160 || in.BasePrice < 0 || in.DeliveryDays < 1 {
		return false
	}
	if in.ServiceType == "" {
		in.ServiceType = "HUMAN"
	}
	if in.ServiceType != "HUMAN" && in.ServiceType != "AI" && in.ServiceType != "HYBRID" {
		return false
	}
	if in.Currency == "" {
		in.Currency = "KRW"
	}
	if len(in.Currency) != 3 {
		return false
	}
	if in.ScopeIncluded == nil {
		in.ScopeIncluded = []any{}
	}
	if in.ScopeExcluded == nil {
		in.ScopeExcluded = []any{}
	}
	if in.Deliverables == nil {
		in.Deliverables = []any{}
	}
	if in.FAQ == nil {
		in.FAQ = []any{}
	}
	if in.Tags == nil {
		in.Tags = []string{}
	}
	for i := range in.Packages {
		pkg := &in.Packages[i]
		if pkg.Type != "BASIC" && pkg.Type != "STANDARD" && pkg.Type != "PREMIUM" && pkg.Type != "CUSTOM" {
			return false
		}
		pkg.Name = strings.TrimSpace(pkg.Name)
		if pkg.Name == "" || pkg.Price < 0 || pkg.DeliveryDays < 1 || pkg.RevisionCount < 0 {
			return false
		}
		if pkg.Features == nil {
			pkg.Features = []any{}
		}
		if pkg.Deliverables == nil {
			pkg.Deliverables = []any{}
		}
	}
	allowedFields := map[string]bool{"text": true, "textarea": true, "select": true, "multi_select": true, "color": true, "file": true, "number": true, "date": true, "boolean": true}
	for i := range in.Requirements {
		requirement := &in.Requirements[i]
		requirement.Label = strings.TrimSpace(requirement.Label)
		if requirement.Label == "" || !allowedFields[requirement.FieldType] {
			return false
		}
		if requirement.Options == nil {
			requirement.Options = []any{}
		}
		if requirement.Validation == nil {
			requirement.Validation = map[string]any{}
		}
	}
	return true
}

var slugInvalid = regexp.MustCompile(`[^a-z0-9가-힣]+`)

func normalizeSlug(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = slugInvalid.ReplaceAllString(value, "-")
	return strings.Trim(value, "-")
}

func (s *Server) createTalent(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	var in talentInput
	if !decodeJSON(w, r, &in) {
		return
	}
	if !validateTalent(&in) {
		writeError(w, 400, "invalid_talent", "상품명, 가격, 납기와 서비스 유형을 확인해 주세요.")
		return
	}
	if in.Slug == "" {
		in.Slug = normalizeSlug(in.Title)
	} else {
		in.Slug = normalizeSlug(in.Slug)
	}
	if in.Slug == "" {
		in.Slug = "talent"
	}
	id := uuid.New()
	if err := s.saveTalent(r, p, id, in, true); err != nil {
		writeError(w, 409, "talent_conflict", "상품을 저장하지 못했습니다. 상품 URL 식별자를 확인해 주세요.")
		return
	}
	s.audit(r, "talent.create", "talent", id.String(), nil, map[string]any{"title": in.Title}, "success")
	writeJSON(w, 201, map[string]any{"id": id, "status": "draft"})
}

func (s *Server) updateTalent(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	id, ok := parseUUIDPath(w, r, "id")
	if !ok {
		return
	}
	var in talentInput
	if !decodeJSON(w, r, &in) {
		return
	}
	if !validateTalent(&in) {
		writeError(w, 400, "invalid_talent", "상품 설정을 확인해 주세요.")
		return
	}
	in.Slug = normalizeSlug(in.Slug)
	if in.Slug == "" {
		in.Slug = normalizeSlug(in.Title)
	}
	var owner uuid.UUID
	if err := s.DB.QueryRow(r.Context(), `SELECT seller_id FROM talents WHERE id=$1`, id).Scan(&owner); err != nil {
		writeError(w, 404, "not_found", "상품을 찾을 수 없습니다.")
		return
	}
	if owner != p.UserID && !hasPermission(p, "orders.manage") {
		writeError(w, 403, "ownership_required", "본인의 상품만 변경할 수 있습니다.")
		return
	}
	if err := s.saveTalent(r, p, id, in, false); err != nil {
		writeError(w, 409, "talent_conflict", "상품을 저장하지 못했습니다.")
		return
	}
	s.audit(r, "talent.update", "talent", id.String(), nil, map[string]any{"title": in.Title}, "success")
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) saveTalent(r *http.Request, p Principal, id uuid.UUID, in talentInput, create bool) error {
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	if create {
		_, err = tx.Exec(r.Context(), `INSERT INTO talents(id,seller_id,category_id,title,slug,summary,description,service_type,base_price,currency,delivery_days,revision_count,scope_included,scope_excluded,deliverables,tags,faq,refund_policy,instant_order,quote_required,subscription_enabled) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21)`, id, p.UserID, in.CategoryID, in.Title, in.Slug, in.Summary, in.Description, in.ServiceType, in.BasePrice, in.Currency, in.DeliveryDays, in.RevisionCount, in.ScopeIncluded, in.ScopeExcluded, in.Deliverables, in.Tags, in.FAQ, in.RefundPolicy, in.InstantOrder, in.QuoteRequired, in.SubscriptionEnabled)
		if err == nil {
			_, _ = tx.Exec(r.Context(), `INSERT INTO seller_profiles(user_id) VALUES($1) ON CONFLICT DO NOTHING`, p.UserID)
			_, _ = tx.Exec(r.Context(), `INSERT INTO user_roles(user_id,role_code) VALUES($1,'seller') ON CONFLICT DO NOTHING`, p.UserID)
		}
	} else {
		_, err = tx.Exec(r.Context(), `UPDATE talents SET category_id=$2,title=$3,slug=$4,summary=$5,description=$6,service_type=$7,base_price=$8,currency=$9,delivery_days=$10,revision_count=$11,scope_included=$12,scope_excluded=$13,deliverables=$14,tags=$15,faq=$16,refund_policy=$17,instant_order=$18,quote_required=$19,subscription_enabled=$20,updated_at=now() WHERE id=$1`, id, in.CategoryID, in.Title, in.Slug, in.Summary, in.Description, in.ServiceType, in.BasePrice, in.Currency, in.DeliveryDays, in.RevisionCount, in.ScopeIncluded, in.ScopeExcluded, in.Deliverables, in.Tags, in.FAQ, in.RefundPolicy, in.InstantOrder, in.QuoteRequired, in.SubscriptionEnabled)
	}
	if err != nil {
		return err
	}
	_, err = tx.Exec(r.Context(), `DELETE FROM talent_packages WHERE talent_id=$1`, id)
	if err == nil {
		for _, pkg := range in.Packages {
			pkgID := uuid.New()
			if pkg.ID != nil {
				pkgID = *pkg.ID
			}
			_, err = tx.Exec(r.Context(), `INSERT INTO talent_packages(id,talent_id,package_type,name,description,price,delivery_days,revision_count,features,deliverables,sort_order,active) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`, pkgID, id, pkg.Type, pkg.Name, pkg.Description, pkg.Price, pkg.DeliveryDays, pkg.RevisionCount, pkg.Features, pkg.Deliverables, pkg.SortOrder, pkg.Active)
			if err != nil {
				break
			}
		}
	}
	if err == nil {
		_, err = tx.Exec(r.Context(), `DELETE FROM talent_requirements WHERE talent_id=$1`, id)
	}
	if err == nil {
		for _, req := range in.Requirements {
			reqID := uuid.New()
			if req.ID != nil {
				reqID = *req.ID
			}
			_, err = tx.Exec(r.Context(), `INSERT INTO talent_requirements(id,talent_id,label,help_text,field_type,required,options,validation,sort_order) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, reqID, id, req.Label, req.HelpText, req.FieldType, req.Required, req.Options, req.Validation, req.SortOrder)
			if err != nil {
				break
			}
		}
	}
	if err != nil {
		return err
	}
	return tx.Commit(r.Context())
}

func (s *Server) getTalent(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDPath(w, r, "id")
	if !ok {
		return
	}
	var result map[string]any
	var raw []byte
	err := s.DB.QueryRow(r.Context(), `SELECT jsonb_build_object('id',t.id,'seller_id',t.seller_id,'category_id',t.category_id,'title',t.title,'slug',t.slug,'summary',t.summary,'description',t.description,'status',t.status,'service_type',t.service_type,'base_price',t.base_price,'currency',t.currency,'delivery_days',t.delivery_days,'revision_count',t.revision_count,'scope_included',t.scope_included,'scope_excluded',t.scope_excluded,'deliverables',t.deliverables,'tags',t.tags,'faq',t.faq,'refund_policy',t.refund_policy,'instant_order',t.instant_order,'quote_required',t.quote_required,'subscription_enabled',t.subscription_enabled,'quality_score',t.quality_score,'seller',jsonb_build_object('id',u.id,'display_name',u.display_name),'packages',COALESCE((SELECT jsonb_agg(to_jsonb(p) ORDER BY p.sort_order) FROM talent_packages p WHERE p.talent_id=t.id),'[]'::jsonb),'requirements',COALESCE((SELECT jsonb_agg(to_jsonb(q) ORDER BY q.sort_order) FROM talent_requirements q WHERE q.talent_id=t.id),'[]'::jsonb)) FROM talents t JOIN users u ON u.id=t.seller_id WHERE t.id=$1 AND (t.status='published' OR t.seller_id=$2 OR $3)`, id, principalID(r), principalHas(r, "orders.manage")).Scan(&raw)
	if err != nil {
		writeError(w, 404, "not_found", "상품을 찾을 수 없습니다.")
		return
	}
	_ = json.Unmarshal(raw, &result)
	writeJSON(w, 200, result)
}
func principalID(r *http.Request) any {
	p, ok := principalFrom(r.Context())
	if !ok {
		return nil
	}
	return p.UserID
}
func principalHas(r *http.Request, permission string) bool {
	p, ok := principalFrom(r.Context())
	return ok && hasPermission(p, permission)
}

func (s *Server) publishTalent(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	id, ok := parseUUIDPath(w, r, "id")
	if !ok {
		return
	}
	tx, err := s.DB.BeginTx(r.Context(), pgx.TxOptions{})
	if err != nil {
		writeError(w, 500, "transaction_failed", "공개 요청을 시작하지 못했습니다.")
		return
	}
	defer tx.Rollback(r.Context())
	var owner uuid.UUID
	var title, status string
	var serviceType, sellerLevel string
	var price int64
	var quality *float64
	err = tx.QueryRow(r.Context(), `SELECT t.seller_id,t.title,t.status,t.service_type,t.base_price,t.quality_score,COALESCE(sp.level,'NEW') FROM talents t LEFT JOIN seller_profiles sp ON sp.user_id=t.seller_id WHERE t.id=$1 FOR UPDATE OF t`, id).Scan(&owner, &title, &status, &serviceType, &price, &quality, &sellerLevel)
	if err != nil {
		writeError(w, 404, "not_found", "상품을 찾을 수 없습니다.")
		return
	}
	if owner != p.UserID && !hasPermission(p, "talents.review") {
		writeError(w, 403, "ownership_required", "본인의 상품만 공개 요청할 수 있습니다.")
		return
	}
	if status == "published" {
		writeJSON(w, 200, map[string]any{"status": "published"})
		return
	}
	var policyID uuid.UUID
	policyRows, queryErr := tx.Query(r.Context(), `SELECT id,conditions FROM approval_policies WHERE resource_type='talent_publish' AND enabled ORDER BY priority`)
	if queryErr != nil {
		writeError(w, 500, "publish_failed", "승인 정책을 확인하지 못했습니다.")
		return
	}
	err = pgx.ErrNoRows
	for policyRows.Next() {
		var candidate uuid.UUID
		var raw []byte
		if policyRows.Scan(&candidate, &raw) != nil {
			continue
		}
		var conditions map[string]any
		if json.Unmarshal(raw, &conditions) == nil && approvalConditionsMatch(conditions, price, serviceType, sellerLevel, quality) {
			policyID = candidate
			err = nil
			break
		}
	}
	policyRows.Close()
	responseStatus := "published"
	if err == pgx.ErrNoRows {
		_, err = tx.Exec(r.Context(), `UPDATE talents SET status='published',published_at=now(),updated_at=now() WHERE id=$1`, id)
		if err == nil {
			_, err = tx.Exec(r.Context(), `INSERT INTO domain_events(id,aggregate_type,aggregate_id,event_type,payload) VALUES($1,'talent',$2,'TalentPublished',$3)`, uuid.New(), id, map[string]any{"automatic": true})
		}
	} else if err == nil {
		responseStatus = "review_pending"
		requestID := uuid.New()
		_, err = tx.Exec(r.Context(), `UPDATE talents SET status='review_pending',updated_at=now() WHERE id=$1`, id)
		if err == nil {
			_, err = tx.Exec(r.Context(), `INSERT INTO approval_requests(id,policy_id,resource_type,resource_id,requested_by,context) VALUES($1,$2,'talent_publish',$3,$4,$5)`, requestID, policyID, id, p.UserID, map[string]any{"title": title})
		}
	}
	if err != nil {
		writeError(w, 500, "publish_failed", "상품 공개 요청을 처리하지 못했습니다.")
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		writeError(w, 500, "publish_failed", "상품 공개 요청을 처리하지 못했습니다.")
		return
	}
	s.audit(r, "talent.publish_request", "talent", id.String(), map[string]any{"status": status}, map[string]any{"status": responseStatus}, "success")
	writeJSON(w, 200, map[string]any{"status": responseStatus, "approval_required": responseStatus == "review_pending"})
}

func approvalConditionsMatch(conditions map[string]any, price int64, serviceType, sellerLevel string, quality *float64) bool {
	if minimum, ok := conditions["min_amount"].(float64); ok && float64(price) < minimum {
		return false
	}
	if maximum, ok := conditions["max_amount"].(float64); ok && float64(price) > maximum {
		return false
	}
	if values, ok := conditions["service_types"].([]any); ok && len(values) > 0 {
		matched := false
		for _, value := range values {
			if text, ok := value.(string); ok && strings.EqualFold(text, serviceType) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	if values, ok := conditions["seller_levels"].([]any); ok && len(values) > 0 {
		matched := false
		for _, value := range values {
			if text, ok := value.(string); ok && strings.EqualFold(text, sellerLevel) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	if threshold, ok := conditions["quality_score_below"].(float64); ok {
		if quality != nil && *quality >= threshold {
			return false
		}
	}
	return true
}

var _ = fmt.Sprint
