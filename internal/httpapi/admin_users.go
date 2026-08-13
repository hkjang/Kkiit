package httpapi

import (
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *Server) listAdminUsers(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	rows, err := s.DB.Query(r.Context(), `SELECT u.id,u.username,u.email,u.display_name,u.status,u.last_login_at,u.created_at,COALESCE(array_agg(ur.role_code ORDER BY ur.role_code) FILTER(WHERE ur.role_code IS NOT NULL),ARRAY[]::text[]) FROM users u LEFT JOIN user_roles ur ON ur.user_id=u.id WHERE $1='' OR u.username ILIKE '%'||$1||'%' OR u.email ILIKE '%'||$1||'%' OR u.display_name ILIKE '%'||$1||'%' GROUP BY u.id ORDER BY u.created_at DESC LIMIT 200`, query)
	if err != nil {
		writeError(w, 500, "query_failed", "사용자를 조회하지 못했습니다.")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id uuid.UUID
		var username, display, status string
		var email *string
		var lastLogin *time.Time
		var created time.Time
		var roles []string
		if rows.Scan(&id, &username, &email, &display, &status, &lastLogin, &created, &roles) == nil {
			items = append(items, map[string]any{"id": id, "username": username, "email": email, "display_name": display, "status": status, "last_login_at": lastLogin, "created_at": created, "roles": roles})
		}
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

func (s *Server) updateAdminUser(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	id, ok := parseUUIDPath(w, r, "id")
	if !ok {
		return
	}
	var in struct {
		Status      string `json:"status"`
		DisplayName string `json:"display_name"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.Status != "active" && in.Status != "suspended" {
		writeError(w, 400, "invalid_status", "active 또는 suspended 상태만 지정할 수 있습니다.")
		return
	}
	if id == p.UserID && in.Status != "active" {
		writeError(w, 409, "self_suspend_denied", "현재 로그인한 관리자 계정은 정지할 수 없습니다.")
		return
	}
	in.DisplayName = strings.TrimSpace(in.DisplayName)
	if in.DisplayName == "" {
		writeError(w, 400, "display_name_required", "표시 이름을 입력해 주세요.")
		return
	}
	tag, err := s.DB.Exec(r.Context(), `UPDATE users SET status=$2,display_name=$3,updated_at=now() WHERE id=$1`, id, in.Status, in.DisplayName)
	if err != nil || tag.RowsAffected() == 0 {
		writeError(w, 404, "user_not_found", "사용자를 찾을 수 없습니다.")
		return
	}
	if in.Status == "suspended" {
		_, _ = s.DB.Exec(r.Context(), `UPDATE sessions SET revoked_at=now() WHERE user_id=$1 AND revoked_at IS NULL`, id)
	}
	s.audit(r, "user.update", "user", id.String(), nil, in, "success")
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) updateAdminUserRoles(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	id, ok := parseUUIDPath(w, r, "id")
	if !ok {
		return
	}
	var in struct {
		Roles []string `json:"roles"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	unique := map[string]bool{}
	for _, role := range in.Roles {
		unique[role] = true
	}
	if id == p.UserID && !unique["super_admin"] {
		writeError(w, 409, "self_admin_removal_denied", "현재 로그인한 계정의 Super Admin 역할은 제거할 수 없습니다.")
		return
	}
	tx, err := s.DB.BeginTx(r.Context(), pgx.TxOptions{})
	if err != nil {
		writeError(w, 500, "transaction_failed", "역할 변경을 시작하지 못했습니다.")
		return
	}
	defer tx.Rollback(r.Context()) //nolint:errcheck
	var hadSuper bool
	if err = tx.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM user_roles WHERE user_id=$1 AND role_code='super_admin')`, id).Scan(&hadSuper); err != nil {
		writeError(w, 404, "user_not_found", "사용자를 찾을 수 없습니다.")
		return
	}
	if hadSuper && !unique["super_admin"] {
		var count int
		if err = tx.QueryRow(r.Context(), `SELECT count(*) FROM user_roles ur JOIN users u ON u.id=ur.user_id WHERE ur.role_code='super_admin' AND u.status='active'`).Scan(&count); err != nil || count <= 1 {
			writeError(w, 409, "last_admin_protected", "마지막 활성 Super Admin 역할은 제거할 수 없습니다.")
			return
		}
	}
	if _, err = tx.Exec(r.Context(), `DELETE FROM user_roles WHERE user_id=$1`, id); err == nil {
		for role := range unique {
			if _, err = tx.Exec(r.Context(), `INSERT INTO user_roles(user_id,role_code,granted_by) VALUES($1,$2,$3)`, id, role, p.UserID); err != nil {
				break
			}
		}
	}
	if err != nil {
		writeError(w, 400, "invalid_role", "역할 목록을 확인해 주세요.")
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		writeError(w, 500, "roles_failed", "역할을 저장하지 못했습니다.")
		return
	}
	s.audit(r, "user.roles_update", "user", id.String(), nil, in, "success")
	writeJSON(w, 200, map[string]any{"ok": true})
}
