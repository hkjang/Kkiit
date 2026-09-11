package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/google/uuid"
)

type sellerProfileInput struct {
	SellerType string         `json:"seller_type"`
	Headline   string         `json:"headline"`
	Biography  string         `json:"biography"`
	Skills     []string       `json:"skills"`
	Capacity   int            `json:"capacity"`
	Settings   map[string]any `json:"settings"`
}

func (s *Server) getMySellerProfile(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	var sellerType, headline, biography, level string
	var skills []string
	var capacity int
	var score float64
	var verified bool
	var settingsRaw []byte
	err := s.DB.QueryRow(r.Context(), `SELECT seller_type,headline,biography,skills,capacity,level,score,verified,settings FROM seller_profiles WHERE user_id=$1`, p.UserID).Scan(&sellerType, &headline, &biography, &skills, &capacity, &level, &score, &verified, &settingsRaw)
	if err == pgx.ErrNoRows {
		writeJSON(w, http.StatusOK, map[string]any{"enabled": false})
		return
	}
	if err != nil {
		writeError(w, 500, "query_failed", "판매자 프로필을 조회하지 못했습니다.")
		return
	}
	var settings any
	_ = json.Unmarshal(settingsRaw, &settings)
	writeJSON(w, 200, map[string]any{"enabled": true, "seller_type": sellerType, "headline": headline, "biography": biography, "skills": skills, "capacity": capacity, "level": level, "score": score, "verified": verified, "settings": settings})
}

func (s *Server) putMySellerProfile(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	var in sellerProfileInput
	if !decodeJSON(w, r, &in) {
		return
	}
	in.Headline = strings.TrimSpace(in.Headline)
	in.Biography = strings.TrimSpace(in.Biography)
	if in.SellerType == "" {
		in.SellerType = "individual"
	}
	if in.SellerType != "individual" && in.SellerType != "business" && in.SellerType != "team" {
		writeError(w, 400, "invalid_seller_type", "판매자 유형을 확인해 주세요.")
		return
	}
	if in.Capacity < 0 || in.Capacity > 10000 {
		writeError(w, 400, "invalid_capacity", "동시 작업 수를 확인해 주세요.")
		return
	}
	if in.Capacity == 0 {
		in.Capacity = 5
	}
	if in.Settings == nil {
		in.Settings = map[string]any{}
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		writeError(w, 500, "transaction_failed", "판매자 전환을 시작하지 못했습니다.")
		return
	}
	defer tx.Rollback(r.Context()) //nolint:errcheck
	if in.Skills == nil {
		// A seller who lists no skills is saying they have none to list, not
		// sending a malformed request.
		in.Skills = []string{}
	}
	_, err = tx.Exec(r.Context(), `INSERT INTO seller_profiles(user_id,seller_type,headline,biography,skills,capacity,settings) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(user_id) DO UPDATE SET seller_type=EXCLUDED.seller_type,headline=EXCLUDED.headline,biography=EXCLUDED.biography,skills=EXCLUDED.skills,capacity=EXCLUDED.capacity,settings=EXCLUDED.settings,updated_at=now()`, p.UserID, in.SellerType, in.Headline, in.Biography, in.Skills, in.Capacity, in.Settings)
	if err == nil {
		_, err = tx.Exec(r.Context(), `INSERT INTO user_roles(user_id,role_code,granted_by) VALUES($1,'seller',$1) ON CONFLICT DO NOTHING`, p.UserID)
	}
	if err != nil {
		writeError(w, 500, "seller_profile_failed", "판매자 프로필을 저장하지 못했습니다.")
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		writeError(w, 500, "seller_profile_failed", "판매자 프로필을 저장하지 못했습니다.")
		return
	}
	s.audit(r, "seller_profile.update", "seller_profile", p.UserID.String(), nil, in, "success")
	writeJSON(w, 200, map[string]any{"enabled": true})
}

// publicSellerProfile is the page a buyer needs before handing someone three
// million won. Until now the only thing about a seller they could see was the
// name and rating embedded in one listing: no other work, no history, no sense
// of whether this person finishes what they start.
//
// Every figure here is derived from completed transactions. The seller writes
// their headline and biography; they do not write their numbers.
func (s *Server) publicSellerProfile(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDPath(w, r, "id")
	if !ok {
		return
	}
	var result []byte
	err := s.DB.QueryRow(r.Context(), `SELECT jsonb_build_object(
			'id',u.id,'display_name',u.display_name,'member_since',u.created_at,
			'headline',COALESCE(sp.headline,''),'biography',COALESCE(sp.biography,''),
			'skills',COALESCE(sp.skills,ARRAY[]::text[]),'seller_type',COALESCE(sp.seller_type,'individual'),
			'verified',COALESCE(sp.verified,false),
			'level',COALESCE(sp.level,'NEW'),'score',COALESCE(sp.score,0),
			'rating',COALESCE(sp.rating,0),'rating_count',COALESCE(sp.rating_count,0),
			'published_talents',(SELECT count(*) FROM talents t WHERE t.seller_id=u.id AND t.status='published'),
			'completed_orders',(SELECT count(*) FROM orders o WHERE o.seller_id=u.id AND o.state='COMPLETED'),
			'active_orders',(SELECT count(*) FROM orders o WHERE o.seller_id=u.id AND o.state NOT IN ('COMPLETED','CANCELLED','REFUNDED')),
			'on_time_orders',(SELECT count(*) FROM orders o WHERE o.seller_id=u.id AND o.state='COMPLETED' AND o.due_at IS NOT NULL AND o.accepted_at IS NOT NULL AND o.accepted_at <= o.due_at),
			'accepting_orders',(COALESCE(sp.capacity,0)<=0 OR (SELECT count(*) FROM orders o WHERE o.seller_id=u.id AND o.state NOT IN ('COMPLETED','CANCELLED','REFUNDED'))<COALESCE(sp.capacity,0))
		) FROM users u LEFT JOIN seller_profiles sp ON sp.user_id=u.id
		WHERE u.id=$1 AND u.status='active' AND EXISTS(SELECT 1 FROM talents t WHERE t.seller_id=u.id AND t.status='published')`, id).Scan(&result)
	if err != nil {
		// A person with nothing published is not a seller as far as the public
		// is concerned, and their account is not something to confirm exists.
		writeError(w, 404, "seller_not_found", "판매자를 찾을 수 없습니다.")
		return
	}
	var payload any
	_ = json.Unmarshal(result, &payload)
	writeJSON(w, 200, payload)
}

// listSellerTalents is the rest of the seller's shelf. A buyer who likes one
// listing usually wants to know what else this person does.
func (s *Server) listSellerTalents(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDPath(w, r, "id")
	if !ok {
		return
	}
	rows, err := s.DB.Query(r.Context(), `SELECT t.id,t.title,t.slug,t.summary,t.service_type,t.base_price,t.currency,t.delivery_days,t.tags,t.quality_score,t.published_at,
			(SELECT count(*) FROM favorites f WHERE f.talent_id=t.id)
		FROM talents t JOIN users u ON u.id=t.seller_id
		WHERE t.seller_id=$1 AND t.status='published' AND u.status='active'
		ORDER BY t.published_at DESC NULLS LAST,t.id DESC LIMIT $2`, id, queryLimit(r, 24, 100))
	if err != nil {
		writeError(w, 500, "query_failed", "판매자 상품을 조회하지 못했습니다.")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var talentID uuid.UUID
		var title, slug, summary, service, currency string
		var price int64
		var days int
		var tags []string
		var quality *float64
		var published *time.Time
		var favorites int64
		if rows.Scan(&talentID, &title, &slug, &summary, &service, &price, &currency, &days, &tags, &quality, &published, &favorites) == nil {
			items = append(items, map[string]any{"id": talentID, "title": title, "slug": slug, "summary": summary,
				"service_type": service, "base_price": price, "currency": currency, "delivery_days": days,
				"tags": tags, "quality_score": quality, "published_at": published, "favorite_count": favorites})
		}
	}
	writeJSON(w, 200, map[string]any{"items": items})
}
