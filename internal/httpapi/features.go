package httpapi

import (
	"context"
	"net/http"
	"strings"
	"time"
)

// featureKeys are the switches that actually gate shipped behaviour. Listing
// them here keeps the admin console from offering a toggle that does nothing.
var featureKeys = map[string]string{
	"ai_matching":       "AI 상품 초안·요구 분석과 추천",
	"smart_quote":       "견적 요청과 견적 비교",
	"enterprise":        "조직 계정과 예산",
	"agent_marketplace": "MCP를 통한 AI Agent 거래",
}

const featureCacheTTL = 10 * time.Second

// features reads the flags with a short cache. A toggle takes effect within
// seconds, which is soon enough for an operator switch and avoids a query on
// every gated request.
func (s *Server) features(ctx context.Context) map[string]bool {
	s.featureMu.Lock()
	if s.featureCache != nil && time.Since(s.featureLoaded) < featureCacheTTL {
		cached := s.featureCache
		s.featureMu.Unlock()
		return cached
	}
	s.featureMu.Unlock()

	loaded := make(map[string]bool, len(featureKeys))
	for key := range featureKeys {
		// A flag row that is missing means the feature was never configured,
		// and the safe reading of that is "available", matching how the product
		// behaved before flags were enforced.
		loaded[key] = true
	}
	rows, err := s.DB.Query(ctx, `SELECT key,enabled FROM feature_flags`)
	if err == nil {
		for rows.Next() {
			var key string
			var enabled bool
			if rows.Scan(&key, &enabled) == nil {
				if _, known := featureKeys[key]; known {
					loaded[key] = enabled
				}
			}
		}
		rows.Close()
	}
	s.featureMu.Lock()
	s.featureCache, s.featureLoaded = loaded, time.Now()
	s.featureMu.Unlock()
	return loaded
}

func (s *Server) featureEnabled(ctx context.Context, key string) bool {
	return s.features(ctx)[key]
}

func (s *Server) invalidateFeatures() {
	s.featureMu.Lock()
	s.featureCache = nil
	s.featureMu.Unlock()
}

// requireFeature turns a flag into a real gate. Hiding a menu is not enough:
// the endpoint has to refuse, or a switched off feature is still reachable.
func (s *Server) requireFeature(key string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.featureEnabled(r.Context(), key) {
			writeError(w, http.StatusNotFound, "feature_disabled", "이 기능은 현재 사용할 수 없습니다.")
			return
		}
		next(w, r)
	}
}

// listFeatures lets the web app hide what the deployment has switched off.
func (s *Server) listFeatures(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"features": s.features(r.Context())})
}

func (s *Server) listFeatureFlags(w http.ResponseWriter, r *http.Request) {
	enabled := s.features(r.Context())
	items := make([]map[string]any, 0, len(featureKeys))
	for key, description := range featureKeys {
		items = append(items, map[string]any{"key": key, "description": description, "enabled": enabled[key]})
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

func (s *Server) updateFeatureFlag(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	key := strings.TrimSpace(r.PathValue("key"))
	if _, known := featureKeys[key]; !known {
		writeError(w, 404, "unknown_feature", "지원하지 않는 기능 플래그입니다.")
		return
	}
	var in struct {
		Enabled bool `json:"enabled"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if _, err := s.DB.Exec(r.Context(), `INSERT INTO feature_flags(key,enabled,description,updated_by) VALUES($1,$2,$3,$4)
		ON CONFLICT(key) DO UPDATE SET enabled=EXCLUDED.enabled,updated_by=EXCLUDED.updated_by,updated_at=now()`,
		key, in.Enabled, featureKeys[key], p.UserID); err != nil {
		writeError(w, 500, "update_failed", "기능 플래그를 저장하지 못했습니다.")
		return
	}
	s.invalidateFeatures()
	s.audit(r, "feature_flag.update", "feature_flag", key, nil, map[string]any{"enabled": in.Enabled}, "success")
	writeJSON(w, 200, map[string]any{"key": key, "enabled": in.Enabled})
}
