package httpapi

import (
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/hkjang/Kkiit/internal/password"
)

// changePassword was missing entirely: the bootstrap administrator's seeded
// credential could never be rotated through the product, and no user could
// replace a password they believed was exposed.
func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	var in struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	var existing *string
	if err := s.DB.QueryRow(r.Context(), `SELECT password_hash FROM users WHERE id=$1 AND status='active'`, p.UserID).Scan(&existing); err != nil {
		writeError(w, 404, "user_not_found", "계정을 찾을 수 없습니다.")
		return
	}
	// An account created through a social provider has no password yet, so the
	// authenticated session is what authorises setting the first one.
	if existing != nil && *existing != "" {
		if !password.Verify(*existing, in.CurrentPassword) {
			time.Sleep(150 * time.Millisecond)
			s.audit(r, "password.change", "user", p.UserID.String(), nil, nil, "failure")
			writeError(w, 403, "invalid_current_password", "현재 비밀번호가 올바르지 않습니다.")
			return
		}
		if in.CurrentPassword == in.NewPassword {
			writeError(w, 400, "password_unchanged", "새 비밀번호가 기존 비밀번호와 같습니다.")
			return
		}
	}
	hash, err := password.Hash(in.NewPassword)
	if err != nil {
		writeError(w, 400, "weak_password", "새 비밀번호는 12자 이상이어야 합니다.")
		return
	}
	if _, err := s.DB.Exec(r.Context(), `UPDATE users SET password_hash=$2,updated_at=now() WHERE id=$1`, p.UserID, hash); err != nil {
		writeError(w, 500, "update_failed", "비밀번호를 저장하지 못했습니다.")
		return
	}
	// Changing a password is how someone responds to a suspected compromise, so
	// every other session ends. The current one stays so the user is not logged
	// out of the page they are on.
	revoked := s.revokeOtherSessions(r, p)
	s.audit(r, "password.change", "user", p.UserID.String(), nil, map[string]any{"revoked_sessions": revoked}, "success")
	writeJSON(w, 200, map[string]any{"ok": true, "revoked_sessions": revoked})
}

func (s *Server) revokeOtherSessions(r *http.Request, p Principal) int64 {
	var current any
	if p.SessionID != nil {
		current = *p.SessionID
	}
	tag, err := s.DB.Exec(r.Context(), `UPDATE sessions SET revoked_at=now() WHERE user_id=$1 AND revoked_at IS NULL AND ($2::uuid IS NULL OR id<>$2)`, p.UserID, current)
	if err != nil {
		return 0
	}
	return tag.RowsAffected()
}

func (s *Server) listMySessions(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	rows, err := s.DB.Query(r.Context(), `SELECT id,ip_address::text,user_agent,created_at,last_seen_at,expires_at
		FROM sessions WHERE user_id=$1 AND revoked_at IS NULL AND expires_at > now() ORDER BY COALESCE(last_seen_at,created_at) DESC LIMIT 50`, p.UserID)
	if err != nil {
		writeError(w, 500, "query_failed", "로그인 기록을 조회하지 못했습니다.")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id uuid.UUID
		var ip, agent *string
		var created, expires time.Time
		var lastSeen *time.Time
		if rows.Scan(&id, &ip, &agent, &created, &lastSeen, &expires) != nil {
			continue
		}
		items = append(items, map[string]any{"id": id, "ip": ip, "user_agent": strings.TrimSpace(deref(agent)),
			"created_at": created, "last_seen_at": lastSeen, "expires_at": expires,
			"current": p.SessionID != nil && *p.SessionID == id})
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

func deref(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// revokeMySessions signs the account out everywhere else. It is the action a
// user needs when they see a session they do not recognise.
func (s *Server) revokeMySessions(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	revoked := s.revokeOtherSessions(r, p)
	s.audit(r, "session.revoke_others", "user", p.UserID.String(), nil, map[string]any{"revoked_sessions": revoked}, "success")
	writeJSON(w, 200, map[string]any{"ok": true, "revoked_sessions": revoked})
}
