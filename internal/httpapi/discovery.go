package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

func (s *Server) addFavorite(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	id, ok := parseUUIDPath(w, r, "id")
	if !ok {
		return
	}
	// Only published products can be saved; a draft would leak its existence.
	tag, err := s.DB.Exec(r.Context(), `INSERT INTO favorites(user_id,talent_id) SELECT $1,t.id FROM talents t JOIN users u ON u.id=t.seller_id WHERE t.id=$2 AND t.status='published' AND u.status='active' ON CONFLICT DO NOTHING`, p.UserID, id)
	if err != nil {
		writeError(w, 500, "favorite_failed", "찜하지 못했습니다.")
		return
	}
	if tag.RowsAffected() == 0 {
		var exists bool
		if s.DB.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM favorites WHERE user_id=$1 AND talent_id=$2)`, p.UserID, id).Scan(&exists) == nil && !exists {
			writeError(w, 404, "talent_not_found", "공개된 상품을 찾을 수 없습니다.")
			return
		}
	}
	writeJSON(w, 200, map[string]any{"favorited": true, "favorite_count": s.favoriteCount(r, id)})
}

func (s *Server) removeFavorite(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	id, ok := parseUUIDPath(w, r, "id")
	if !ok {
		return
	}
	if _, err := s.DB.Exec(r.Context(), `DELETE FROM favorites WHERE user_id=$1 AND talent_id=$2`, p.UserID, id); err != nil {
		writeError(w, 500, "favorite_failed", "찜을 해제하지 못했습니다.")
		return
	}
	writeJSON(w, 200, map[string]any{"favorited": false, "favorite_count": s.favoriteCount(r, id)})
}

func (s *Server) favoriteCount(r *http.Request, talentID uuid.UUID) int64 {
	var count int64
	if err := s.DB.QueryRow(r.Context(), `SELECT count(*) FROM favorites WHERE talent_id=$1`, talentID).Scan(&count); err != nil {
		return 0
	}
	return count
}

func (s *Server) listMyFavorites(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	rows, err := s.DB.Query(r.Context(), `SELECT t.id,t.title,t.slug,t.summary,t.service_type,t.base_price,t.currency,t.delivery_days,t.tags,t.quality_score,t.status,t.published_at,
		u.id,u.display_name,COALESCE(sp.level,'NEW'),COALESCE(sp.score,0),COALESCE(sp.rating,0),COALESCE(sp.rating_count,0),f.created_at
		FROM favorites f JOIN talents t ON t.id=f.talent_id JOIN users u ON u.id=t.seller_id LEFT JOIN seller_profiles sp ON sp.user_id=u.id
		WHERE f.user_id=$1 ORDER BY f.created_at DESC LIMIT $2`, p.UserID, queryLimit(r, 50, 200))
	if err != nil {
		writeError(w, 500, "query_failed", "찜한 서비스를 조회하지 못했습니다.")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id, sellerID uuid.UUID
		var title, slug, summary, service, currency, display, level, status string
		var price int64
		var days int
		var tags []string
		var quality *float64
		var published *time.Time
		var favorited time.Time
		var score, rating float64
		var ratingCount int
		if rows.Scan(&id, &title, &slug, &summary, &service, &price, &currency, &days, &tags, &quality, &status, &published,
			&sellerID, &display, &level, &score, &rating, &ratingCount, &favorited) != nil {
			continue
		}
		items = append(items, map[string]any{"id": id, "title": title, "slug": slug, "summary": summary, "service_type": service,
			"base_price": price, "currency": currency, "delivery_days": days, "tags": tags, "quality_score": quality,
			"status": status, "published_at": published, "favorited": true, "favorited_at": favorited,
			"seller": map[string]any{"id": sellerID, "display_name": display, "level": level, "score": score, "rating": rating, "rating_count": ratingCount}})
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

type portfolioInput struct {
	Title       string           `json:"title"`
	Description string           `json:"description"`
	Media       []map[string]any `json:"media"`
	Tags        []string         `json:"tags"`
}

func (in *portfolioInput) validate() (string, bool) {
	in.Title = strings.TrimSpace(in.Title)
	in.Description = strings.TrimSpace(in.Description)
	if len([]rune(in.Title)) < 2 || len([]rune(in.Title)) > 160 {
		return "포트폴리오 제목을 2자 이상 160자 이하로 입력해 주세요.", false
	}
	if len([]rune(in.Description)) > 4000 {
		return "설명은 4,000자 이하로 입력해 주세요.", false
	}
	if in.Media == nil {
		in.Media = []map[string]any{}
	}
	if len(in.Media) > 12 {
		return "미디어는 12개까지 첨부할 수 있습니다.", false
	}
	if in.Tags == nil {
		in.Tags = []string{}
	}
	if len(in.Tags) > 20 {
		return "태그는 20개까지 등록할 수 있습니다.", false
	}
	return "", true
}

func (s *Server) listMyPortfolios(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	s.writePortfolios(w, r, p.UserID)
}

// listSellerPortfolios is public so a buyer can judge a seller's work before
// signing in. Only sellers with a public profile expose one.
func (s *Server) listSellerPortfolios(w http.ResponseWriter, r *http.Request) {
	sellerID, ok := parseUUIDPath(w, r, "id")
	if !ok {
		return
	}
	s.writePortfolios(w, r, sellerID)
}

func (s *Server) writePortfolios(w http.ResponseWriter, r *http.Request, owner uuid.UUID) {
	rows, err := s.DB.Query(r.Context(), `SELECT id,title,description,media,tags,created_at,updated_at FROM portfolios WHERE owner_id=$1 ORDER BY created_at DESC LIMIT $2`, owner, queryLimit(r, 50, 100))
	if err != nil {
		writeError(w, 500, "query_failed", "포트폴리오를 조회하지 못했습니다.")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id uuid.UUID
		var title, description string
		var mediaRaw []byte
		var tags []string
		var created, updated time.Time
		if rows.Scan(&id, &title, &description, &mediaRaw, &tags, &created, &updated) != nil {
			continue
		}
		var media any
		_ = json.Unmarshal(mediaRaw, &media)
		items = append(items, map[string]any{"id": id, "title": title, "description": description, "media": media, "tags": tags, "created_at": created, "updated_at": updated})
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

func (s *Server) createMyPortfolio(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	var in portfolioInput
	if !decodeJSON(w, r, &in) {
		return
	}
	if message, ok := in.validate(); !ok {
		writeError(w, 400, "invalid_portfolio", message)
		return
	}
	id := uuid.New()
	if _, err := s.DB.Exec(r.Context(), `INSERT INTO portfolios(id,owner_id,title,description,media,tags) VALUES($1,$2,$3,$4,$5,$6)`,
		id, p.UserID, in.Title, in.Description, in.Media, in.Tags); err != nil {
		writeError(w, 500, "create_failed", "포트폴리오를 저장하지 못했습니다.")
		return
	}
	s.audit(r, "portfolio.create", "portfolio", id.String(), nil, map[string]any{"title": in.Title}, "success")
	writeJSON(w, 201, map[string]any{"id": id})
}

func (s *Server) updateMyPortfolio(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	id, ok := parseUUIDPath(w, r, "id")
	if !ok {
		return
	}
	var in portfolioInput
	if !decodeJSON(w, r, &in) {
		return
	}
	if message, valid := in.validate(); !valid {
		writeError(w, 400, "invalid_portfolio", message)
		return
	}
	tag, err := s.DB.Exec(r.Context(), `UPDATE portfolios SET title=$3,description=$4,media=$5,tags=$6,updated_at=now() WHERE id=$1 AND owner_id=$2`,
		id, p.UserID, in.Title, in.Description, in.Media, in.Tags)
	if err != nil || tag.RowsAffected() == 0 {
		writeError(w, 404, "portfolio_not_found", "포트폴리오를 찾을 수 없습니다.")
		return
	}
	s.audit(r, "portfolio.update", "portfolio", id.String(), nil, map[string]any{"title": in.Title}, "success")
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) deleteMyPortfolio(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	id, ok := parseUUIDPath(w, r, "id")
	if !ok {
		return
	}
	tag, err := s.DB.Exec(r.Context(), `DELETE FROM portfolios WHERE id=$1 AND owner_id=$2`, id, p.UserID)
	if err != nil || tag.RowsAffected() == 0 {
		writeError(w, 404, "portfolio_not_found", "포트폴리오를 찾을 수 없습니다.")
		return
	}
	s.audit(r, "portfolio.delete", "portfolio", id.String(), nil, nil, "success")
	w.WriteHeader(204)
}
