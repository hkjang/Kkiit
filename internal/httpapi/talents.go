package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/hkjang/Kkiit/internal/cryptox"
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
	Options             []optionInput      `json:"options"`
	Requirements        []requirementInput `json:"requirements"`
}

// optionInput is a paid add on the seller defines. Order pricing reads the
// stored price, never a price supplied by the buyer's client.
type optionInput struct {
	ID             *uuid.UUID `json:"id,omitempty"`
	Name           string     `json:"name"`
	Description    string     `json:"description"`
	Price          int64      `json:"price"`
	AdditionalDays int        `json:"additional_days"`
	SortOrder      int        `json:"sort_order"`
	Active         bool       `json:"active"`
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
	// The numbers a seller needs to act sit next to the listing they belong to.
	// Views and orders together say whether a listing is not being found or is
	// being found and passed over, which call for opposite fixes.
	rows, err := s.DB.Query(r.Context(), `SELECT t.id,t.title,t.slug,t.status,t.service_type,t.base_price,t.currency,t.delivery_days,t.updated_at,
			(SELECT count(*) FROM tracked_events e WHERE e.event_name='talent_view' AND e.resource_id=t.id AND e.occurred_at > now()-interval '30 days'),
			(SELECT count(*) FROM orders o WHERE o.talent_id=t.id AND o.created_at > now()-interval '30 days'),
			(SELECT count(*) FROM orders o WHERE o.talent_id=t.id),
			(SELECT count(*) FROM orders o WHERE o.talent_id=t.id AND o.state='COMPLETED'),
			(SELECT count(*) FROM favorites f WHERE f.talent_id=t.id),
			COALESCE(t.quality_score,0)
		FROM talents t WHERE t.seller_id=$1 ORDER BY t.updated_at DESC`, p.UserID)
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
		var views, orders30, ordersTotal, completed, favorites int64
		var quality float64
		if rows.Scan(&id, &title, &slug, &status, &service, &price, &currency, &days, &updated, &views, &orders30, &ordersTotal, &completed, &favorites, &quality) == nil {
			// A conversion rate with no views behind it is noise, so it is only
			// reported once there is something to divide by.
			var conversion *float64
			if views > 0 {
				rate := float64(orders30) / float64(views) * 100
				conversion = &rate
			}
			items = append(items, map[string]any{"id": id, "title": title, "slug": slug, "status": status, "service_type": service,
				"base_price": price, "currency": currency, "delivery_days": days, "updated_at": updated,
				"views_30d": views, "orders_30d": orders30, "orders_total": ordersTotal, "completed_orders": completed,
				"favorite_count": favorites, "quality_score": quality, "conversion_rate": conversion})
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
	// Browsing stopped at whatever the first page held, with no way to narrow it
	// and no way to reach anything past it. Ranked results cannot use a keyset
	// cursor because the sort key is computed per request, so the window moves by
	// offset instead, bounded so nobody can walk the whole table one page at a
	// time.
	offset := 0
	if raw := r.URL.Query().Get("offset"); raw != "" {
		if parsed, e := strconv.Atoi(raw); e == nil && parsed > 0 {
			offset = parsed
			if offset > 1000 {
				offset = 1000
			}
		}
	}
	// A category filter includes the children of the chosen category: someone who
	// picks 디자인 wants logo work too, not only listings filed at the top level.
	category := strings.TrimSpace(r.URL.Query().Get("category"))
	weights := s.searchWeights(r)
	// One extra row answers "is there more" without a second counting query over
	// the whole catalogue.
	args := []any{query, limit + 1, weights.Text, weights.Quality, weights.Recency, weights.HalfLifeDays, principalID(r)}
	filters := ""
	// Arguments are only bound when their filter is present; PostgreSQL rejects a
	// statement that is handed a parameter it never mentions.
	bind := func(value any) string {
		args = append(args, value)
		return "$" + strconv.Itoa(len(args))
	}
	if category != "" {
		slug := bind(category)
		filters += ` AND t.category_id IN (SELECT c.id FROM categories c WHERE c.slug=` + slug + ` OR c.parent_id=(SELECT id FROM categories WHERE slug=` + slug + `))`
	}
	if value, err := strconv.ParseInt(r.URL.Query().Get("price_min"), 10, 64); err == nil && value > 0 {
		filters += ` AND t.base_price>=` + bind(value)
	}
	if value, err := strconv.ParseInt(r.URL.Query().Get("price_max"), 10, 64); err == nil && value > 0 {
		filters += ` AND t.base_price<=` + bind(value)
	}
	if value, err := strconv.Atoi(r.URL.Query().Get("max_delivery_days")); err == nil && value > 0 {
		filters += ` AND t.delivery_days<=` + bind(value)
	}
	if serviceType := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("service_type"))); serviceType == "HUMAN" || serviceType == "AI" || serviceType == "HYBRID" {
		filters += ` AND t.service_type=` + bind(serviceType)
	}
	// The sort is chosen from a fixed set rather than interpolated, so no caller
	// supplied text ever reaches the ORDER BY.
	order := "rank_score DESC,t.published_at DESC"
	switch r.URL.Query().Get("sort") {
	case "price_asc":
		order = "t.base_price ASC,t.published_at DESC"
	case "price_desc":
		order = "t.base_price DESC,t.published_at DESC"
	case "newest":
		order = "t.published_at DESC"
	case "delivery":
		order = "t.delivery_days ASC,rank_score DESC"
	case "rating":
		order = "COALESCE(sp.rating,0) DESC,COALESCE(sp.rating_count,0) DESC,rank_score DESC"
	}
	// Text relevance alone ranks a brand new empty listing next to a proven one,
	// so the computed quality score and freshness share the ordering.
	//
	// Browsing and searching are different queries and are built as such. Folding
	// them into one `($1='' OR ...)` predicate stopped PostgreSQL from proving
	// the text indexes applicable, and a browse-shaped plan was then used for
	// every search: measured on 100k published talents, 57ms of sequential scan
	// against 3.2ms once each branch could reach its own index.
	match := ""
	if query != "" {
		match = ` AND (t.search_document @@ websearch_to_tsquery('simple',$1) OR t.title ILIKE '%'||$1||'%')`
	}
	rows, err := s.DB.Query(r.Context(), `SELECT t.id,t.title,t.slug,t.summary,t.service_type,t.base_price,t.currency,t.delivery_days,t.tags,t.quality_score,t.published_at,u.id,u.display_name,COALESCE(sp.level,'NEW'),COALESCE(sp.score,0),COALESCE(sp.rating,0),COALESCE(sp.rating_count,0),
		(SELECT count(*) FROM favorites f WHERE f.talent_id=t.id),
		($7::uuid IS NOT NULL AND EXISTS(SELECT 1 FROM favorites f WHERE f.talent_id=t.id AND f.user_id=$7)),
		(COALESCE(sp.capacity,0)<=0 OR (SELECT count(*) FROM orders o WHERE o.seller_id=t.seller_id AND o.state NOT IN ('COMPLETED','CANCELLED','REFUNDED'))<COALESCE(sp.capacity,0)),
		($3*CASE WHEN $1='' THEN 0 ELSE LEAST(1,ts_rank(t.search_document,websearch_to_tsquery('simple',$1))*10) END
		 +$4*COALESCE(t.quality_score,50)/100
		 +$5/(1+COALESCE(EXTRACT(EPOCH FROM (now()-t.published_at))/86400,0)/$6)) AS rank_score
		FROM talents t JOIN users u ON u.id=t.seller_id LEFT JOIN seller_profiles sp ON sp.user_id=u.id
		WHERE t.status='published' AND u.status='active'`+match+filters+`
		ORDER BY `+order+`,t.id DESC LIMIT $2 OFFSET `+strconv.Itoa(offset), args...)
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
		var score, rating, rank float64
		var ratingCount int
		var favoriteCount int64
		var favorited, accepting bool
		if rows.Scan(&id, &title, &slug, &summary, &service, &price, &currency, &days, &tags, &quality, &published, &sellerID, &display, &level, &score, &rating, &ratingCount, &favoriteCount, &favorited, &accepting, &rank) == nil {
			items = append(items, map[string]any{"id": id, "title": title, "slug": slug, "summary": summary, "service_type": service, "base_price": price, "currency": currency, "delivery_days": days, "tags": tags, "quality_score": quality, "published_at": published, "rank_score": rank, "favorite_count": favoriteCount, "favorited": favorited, "accepting_orders": accepting, "seller": map[string]any{"id": sellerID, "display_name": display, "level": level, "score": score, "rating": rating, "rating_count": ratingCount}})
		}
	}
	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	writeJSON(w, 200, map[string]any{"items": items, "query": query, "offset": offset, "has_more": hasMore, "next_offset": offset + len(items)})
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
	for index := range in.Options {
		in.Options[index].Name = strings.TrimSpace(in.Options[index].Name)
		if in.Options[index].Name == "" || len([]rune(in.Options[index].Name)) > 100 ||
			in.Options[index].Price < 0 || in.Options[index].AdditionalDays < 0 || in.Options[index].AdditionalDays > 365 {
			return false
		}
	}
	if in.Options == nil {
		in.Options = []optionInput{}
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

type searchWeights struct {
	Text, Quality, Recency, HalfLifeDays float64
}

// searchWeights reads the operator tuned ranking blend, falling back to a split
// that still favours relevance when the setting is missing or malformed.
func (s *Server) searchWeights(r *http.Request) searchWeights {
	weights := searchWeights{Text: 0.5, Quality: 0.35, Recency: 0.15, HalfLifeDays: 30}
	setting, err := s.settingObject(r, "search.policy")
	if err != nil {
		return weights
	}
	read := func(key string, current float64, min, max float64) float64 {
		value, ok := setting[key].(float64)
		if !ok || value < min || value > max {
			return current
		}
		return value
	}
	weights.Text = read("text_weight", weights.Text, 0, 1)
	weights.Quality = read("quality_weight", weights.Quality, 0, 1)
	weights.Recency = read("recency_weight", weights.Recency, 0, 1)
	weights.HalfLifeDays = read("recency_half_life_days", weights.HalfLifeDays, 1, 3650)
	if weights.Text+weights.Quality+weights.Recency == 0 {
		return searchWeights{Text: 0.5, Quality: 0.35, Recency: 0.15, HalfLifeDays: weights.HalfLifeDays}
	}
	return weights
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
	var status, sellerLevel string
	var before talentInput
	var quality *float64
	err := s.DB.QueryRow(r.Context(), `SELECT t.seller_id,t.status,t.title,t.summary,t.description,t.service_type,t.base_price,t.quality_score,COALESCE(sp.level,'NEW')
		FROM talents t LEFT JOIN seller_profiles sp ON sp.user_id=t.seller_id WHERE t.id=$1`, id).
		Scan(&owner, &status, &before.Title, &before.Summary, &before.Description, &before.ServiceType, &before.BasePrice, &quality, &sellerLevel)
	if err != nil {
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
	// A published product that changes in a way a reviewer would care about goes
	// back through the same approval policies it passed the first time.
	responseStatus := status
	if status == "published" && materialTalentChange(before, in) {
		newStatus, reviewErr := s.requeueForReview(r, p, id, in)
		if reviewErr != nil {
			writeError(w, 500, "review_failed", "변경 사항의 검토 상태를 저장하지 못했습니다.")
			return
		}
		responseStatus = newStatus
	}
	s.audit(r, "talent.update", "talent", id.String(), map[string]any{"status": status, "title": before.Title, "base_price": before.BasePrice},
		map[string]any{"status": responseStatus, "title": in.Title, "base_price": in.BasePrice}, "success")
	writeJSON(w, 200, map[string]any{"ok": true, "status": responseStatus})
}

// availableSlug keeps the public URL readable when two sellers pick the same
// product title. Without it the second seller simply cannot save the product.
func availableSlug(ctx context.Context, tx pgx.Tx, base string, self uuid.UUID) (string, error) {
	candidate := base
	for attempt := 2; attempt < 100; attempt++ {
		var taken bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM talents WHERE slug=$1 AND id<>$2)`, candidate, self).Scan(&taken); err != nil {
			return "", err
		}
		if !taken {
			return candidate, nil
		}
		candidate = base + "-" + strconv.Itoa(attempt)
	}
	suffix, err := cryptox.RandomToken(4)
	if err != nil {
		return "", err
	}
	return base + "-" + strings.ToLower(suffix), nil
}

func (s *Server) saveTalent(r *http.Request, p Principal, id uuid.UUID, in talentInput, create bool) error {
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	if in.Slug, err = availableSlug(r.Context(), tx, in.Slug, id); err != nil {
		return err
	}
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
		// Options are replaced wholesale like packages. Existing orders keep the
		// price they were charged, so removing one never rewrites history.
		_, err = tx.Exec(r.Context(), `DELETE FROM talent_options WHERE talent_id=$1`, id)
	}
	if err == nil {
		for _, option := range in.Options {
			optionID := uuid.New()
			if option.ID != nil {
				optionID = *option.ID
			}
			_, err = tx.Exec(r.Context(), `INSERT INTO talent_options(id,talent_id,name,description,price,additional_days,sort_order,active) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`,
				optionID, id, option.Name, option.Description, option.Price, option.AdditionalDays, option.SortOrder, option.Active)
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
	err := s.DB.QueryRow(r.Context(), `SELECT jsonb_build_object('id',t.id,'seller_id',t.seller_id,'category_id',t.category_id,'category',(SELECT jsonb_build_object('slug',c.slug,'name',c.name) FROM categories c WHERE c.id=t.category_id),'title',t.title,'slug',t.slug,'summary',t.summary,'description',t.description,'status',t.status,'service_type',t.service_type,'base_price',t.base_price,'currency',t.currency,'delivery_days',t.delivery_days,'revision_count',t.revision_count,'scope_included',t.scope_included,'scope_excluded',t.scope_excluded,'deliverables',t.deliverables,'tags',t.tags,'faq',t.faq,'refund_policy',t.refund_policy,'instant_order',t.instant_order,'quote_required',t.quote_required,'subscription_enabled',t.subscription_enabled,'quality_score',t.quality_score,'favorite_count',(SELECT count(*) FROM favorites f WHERE f.talent_id=t.id),'favorited',($2::uuid IS NOT NULL AND EXISTS(SELECT 1 FROM favorites f WHERE f.talent_id=t.id AND f.user_id=$2)),'accepting_orders',(COALESCE(sp.capacity,0)<=0 OR (SELECT count(*) FROM orders o WHERE o.seller_id=t.seller_id AND o.state NOT IN ('COMPLETED','CANCELLED','REFUNDED'))<COALESCE(sp.capacity,0)),'seller',jsonb_build_object('id',u.id,'display_name',u.display_name,'level',COALESCE(sp.level,'NEW'),'score',COALESCE(sp.score,0),'rating',COALESCE(sp.rating,0),'rating_count',COALESCE(sp.rating_count,0),'headline',COALESCE(sp.headline,'')),'packages',COALESCE((SELECT jsonb_agg(to_jsonb(p) ORDER BY p.sort_order) FROM talent_packages p WHERE p.talent_id=t.id),'[]'::jsonb),'options',COALESCE((SELECT jsonb_agg(to_jsonb(o) ORDER BY o.sort_order) FROM talent_options o WHERE o.talent_id=t.id AND o.active),'[]'::jsonb),'requirements',COALESCE((SELECT jsonb_agg(to_jsonb(q) ORDER BY q.sort_order) FROM talent_requirements q WHERE q.talent_id=t.id),'[]'::jsonb)) FROM talents t JOIN users u ON u.id=t.seller_id LEFT JOIN seller_profiles sp ON sp.user_id=t.seller_id WHERE t.id=$1 AND ((t.status='published' AND u.status='active') OR t.seller_id=$2 OR $3)`, id, principalID(r), principalHas(r, "orders.manage")).Scan(&raw)
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
	policyID, err := matchingApprovalPolicy(r.Context(), tx, price, serviceType, sellerLevel, quality)
	if err != nil && err != pgx.ErrNoRows {
		writeError(w, 500, "publish_failed", "승인 정책을 확인하지 못했습니다.")
		return
	}
	responseStatus := "published"
	if err == pgx.ErrNoRows {
		_, err = tx.Exec(r.Context(), `UPDATE talents SET status='published',published_at=now(),updated_at=now() WHERE id=$1`, id)
		if err == nil {
			err = emitEvent(r.Context(), tx, "talent", id, "TalentPublished", map[string]any{"automatic": true, "talent_title": title})
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

// matchingApprovalPolicy returns the first enabled policy whose conditions the
// product meets, or pgx.ErrNoRows when nothing applies and it can go straight
// out. Both publishing and editing a published product use it so the same
// conditions decide in either case.
func matchingApprovalPolicy(ctx context.Context, tx pgx.Tx, price int64, serviceType, sellerLevel string, quality *float64) (uuid.UUID, error) {
	rows, err := tx.Query(ctx, `SELECT id,conditions FROM approval_policies WHERE resource_type='talent_publish' AND enabled ORDER BY priority`)
	if err != nil {
		return uuid.Nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var candidate uuid.UUID
		var raw []byte
		if rows.Scan(&candidate, &raw) != nil {
			continue
		}
		var conditions map[string]any
		if json.Unmarshal(raw, &conditions) == nil && approvalConditionsMatch(conditions, price, serviceType, sellerLevel, quality) {
			return candidate, nil
		}
	}
	if err := rows.Err(); err != nil {
		return uuid.Nil, err
	}
	return uuid.Nil, pgx.ErrNoRows
}

// materialTalentChange reports whether an edit changes what a reviewer would
// have judged. Editing a published product used to bypass approval entirely: a
// seller could get something harmless approved and then rewrite it.
func materialTalentChange(before, after talentInput) bool {
	return before.Title != after.Title || before.Summary != after.Summary || before.Description != after.Description ||
		before.ServiceType != after.ServiceType || before.BasePrice != after.BasePrice
}

// requeueForReview unpublishes an edited product when an approval policy covers
// it and opens a review request. When no policy matches, the edit stays live,
// which is the same rule publishing follows.
func (s *Server) requeueForReview(r *http.Request, p Principal, id uuid.UUID, in talentInput) (string, error) {
	tx, err := s.DB.BeginTx(r.Context(), pgx.TxOptions{})
	if err != nil {
		return "", err
	}
	defer tx.Rollback(r.Context()) //nolint:errcheck
	var sellerLevel string
	var quality *float64
	if err := tx.QueryRow(r.Context(), `SELECT t.quality_score,COALESCE(sp.level,'NEW') FROM talents t LEFT JOIN seller_profiles sp ON sp.user_id=t.seller_id WHERE t.id=$1 FOR UPDATE OF t`, id).Scan(&quality, &sellerLevel); err != nil {
		return "", err
	}
	policyID, err := matchingApprovalPolicy(r.Context(), tx, in.BasePrice, in.ServiceType, sellerLevel, quality)
	if err == pgx.ErrNoRows {
		return "published", nil
	}
	if err != nil {
		return "", err
	}
	if _, err := tx.Exec(r.Context(), `UPDATE talents SET status='review_pending',updated_at=now() WHERE id=$1`, id); err != nil {
		return "", err
	}
	if _, err := tx.Exec(r.Context(), `INSERT INTO approval_requests(id,policy_id,resource_type,resource_id,requested_by,context) VALUES($1,$2,'talent_publish',$3,$4,$5)`,
		uuid.New(), policyID, id, p.UserID, map[string]any{"title": in.Title, "reason": "material_edit"}); err != nil {
		return "", err
	}
	if err := tx.Commit(r.Context()); err != nil {
		return "", err
	}
	return "review_pending", nil
}

// setMyTalentStatus gives a seller the one control over their own listing they
// did not have: taking it off the market. Until now only an operator could
// pause a listing, so a seller who went on holiday, lost a subcontractor or
// simply priced something wrong had no way to stop new orders arriving.
//
// Pausing stops new orders and nothing else. Orders already under way keep
// their deadlines and their escrow, because a seller stepping back from selling
// is not a reason to abandon work a buyer already paid for.
func (s *Server) setMyTalentStatus(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	id, ok := parseUUIDPath(w, r, "id")
	if !ok {
		return
	}
	var in struct {
		Status string `json:"status"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	in.Status = strings.ToLower(strings.TrimSpace(in.Status))
	var owner uuid.UUID
	var current string
	if err := s.DB.QueryRow(r.Context(), `SELECT seller_id,status FROM talents WHERE id=$1`, id).Scan(&owner, &current); err != nil {
		writeError(w, 404, "not_found", "상품을 찾을 수 없습니다.")
		return
	}
	if owner != p.UserID {
		writeError(w, 403, "ownership_required", "본인의 상품만 변경할 수 있습니다.")
		return
	}
	// Resuming a paused listing does not go back through review: the content is
	// the same content that was approved. Editing it is what sends it back, and
	// that path already exists.
	allowed := map[string][]string{
		"published": {"paused"},
		"paused":    {"published"},
		"draft":     {"archived"},
		"archived":  {"published", "paused", "draft"},
	}
	permitted := false
	for _, from := range allowed[in.Status] {
		if from == current {
			permitted = true
		}
	}
	if !permitted {
		writeError(w, 409, "status_transition_denied", fmt.Sprintf("%s 상태의 상품을 %s(으)로 바꿀 수 없습니다.", current, in.Status))
		return
	}
	if _, err := s.DB.Exec(r.Context(), `UPDATE talents SET status=$2,updated_at=now() WHERE id=$1`, id, in.Status); err != nil {
		writeError(w, 500, "update_failed", "상품 상태를 저장하지 못했습니다.")
		return
	}
	s.audit(r, "talent.status", "talent", id.String(), map[string]any{"status": current}, map[string]any{"status": in.Status}, "success")
	writeJSON(w, 200, map[string]any{"id": id, "status": in.Status})
}

// recordTalentView is what turns "nobody is ordering" into an answerable
// question. It is deliberately cheap and deliberately conservative: a view is
// counted once per viewer per listing per day, the seller's own visits never
// count, and an anonymous viewer is identified by a hash rather than by storing
// their address.
func (s *Server) recordTalentView(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDPath(w, r, "id")
	if !ok {
		return
	}
	var seller uuid.UUID
	var status string
	if err := s.DB.QueryRow(r.Context(), `SELECT seller_id,status FROM talents WHERE id=$1`, id).Scan(&seller, &status); err != nil {
		writeError(w, 404, "not_found", "상품을 찾을 수 없습니다.")
		return
	}
	p, signedIn := principalFrom(r.Context())
	if status != "published" || (signedIn && p.UserID == seller) {
		// Not an error for the caller: the page rendered fine either way.
		w.WriteHeader(http.StatusNoContent)
		return
	}
	var userID any
	if signedIn && p.UserID != uuid.Nil {
		userID = p.UserID
	}
	sessionKey := viewerFingerprint(r)
	_, err := s.DB.Exec(r.Context(), `INSERT INTO tracked_events(id,user_id,session_key,event_name,resource_type,resource_id)
		VALUES($1,$2,$3,'talent_view','talent',$4) ON CONFLICT DO NOTHING`, uuid.New(), userID, sessionKey, id)
	if err != nil {
		// Analytics must never break the page it is measuring.
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// viewerFingerprint identifies an anonymous viewer for a day without keeping
// anything that identifies the person. It is a hash, it is only ever compared
// with itself, and it is not reversible into an address.
func viewerFingerprint(r *http.Request) string {
	sum := sha256.Sum256([]byte(clientIP(r).String() + "\n" + r.UserAgent()))
	return hex.EncodeToString(sum[:16])
}
