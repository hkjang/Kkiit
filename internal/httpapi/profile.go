package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
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
