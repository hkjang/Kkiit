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

// BeginDrain marks the process as going away. Readiness fails from this moment
// while the server keeps serving what is already in flight, which is the only
// way a load balancer learns to stop sending new work here before the socket
// closes. Without it a rolling deploy hands requests to a process that is
// already shutting down and the caller sees a connection error.
func (s *Server) BeginDrain() {
	s.draining.Store(true)
}

func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	if s.draining.Load() {
		writeError(w, http.StatusServiceUnavailable, "shutting_down", "종료 중이라 새 요청을 받지 않습니다.")
		return
	}
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
