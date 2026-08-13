package httpapi

import (
	"encoding/json"
	"net/http"
	"runtime"
	"time"
)

func (s *Server) live(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := contextWithTimeout(r, 2*time.Second)
	defer cancel()
	if err := s.DB.Ping(ctx); err != nil {
		writeError(w, http.StatusServiceUnavailable, "database_unavailable", "데이터베이스 연결을 확인해 주세요.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ready"})
}

func (s *Server) version(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"service": "Kkiit", "version": s.Version, "commit": s.Commit, "built_at": s.BuiltAt,
		"go_version": runtime.Version(), "api_version": "v1", "mcp_protocol_version": "2025-11-25",
	})
}

func (s *Server) settingObject(r *http.Request, key string) (map[string]any, error) {
	var raw []byte
	if err := s.DB.QueryRow(r.Context(), `SELECT value FROM system_settings WHERE key=$1`, key).Scan(&raw); err != nil {
		return nil, err
	}
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	return value, nil
}
