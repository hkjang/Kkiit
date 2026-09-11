package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *Server) listAdminUsers(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	limit := queryLimit(r, 100, 500)
	cursorAt, cursorID := requestCursor(r)
	rows, err := s.DB.Query(r.Context(), `SELECT u.id,u.username,u.email,u.display_name,u.status,u.last_login_at,u.created_at,COALESCE(array_agg(ur.role_code ORDER BY ur.role_code) FILTER(WHERE ur.role_code IS NOT NULL),ARRAY[]::text[])
		FROM users u LEFT JOIN user_roles ur ON ur.user_id=u.id
		WHERE ($1='' OR u.username ILIKE '%'||$1||'%' OR u.email ILIKE '%'||$1||'%' OR u.display_name ILIKE '%'||$1||'%')
			AND ($3::timestamptz IS NULL OR (u.created_at,u.id) < ($3,$4))
		GROUP BY u.id ORDER BY u.created_at DESC,u.id DESC LIMIT $2`, query, limit+1, cursorAt, cursorID)
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
	items, next := pageResult(items, limit, func(item map[string]any) (time.Time, uuid.UUID) {
		return timeField(item, "created_at"), uuidField(item, "id")
	})
	writeJSON(w, 200, map[string]any{"items": items, "next_cursor": next})
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
	// A suspended account cannot sign in to read a notification, so the login
	// refusal is where that news belongs. Coming back is worth telling them.
	if in.Status == "active" {
		s.notifyAccountChange(r, id, "AccountReactivated", nil)
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
	// Losing a role changes what someone is allowed to do, and a seller whose
	// selling role is taken away would otherwise watch their listings stop
	// working with no explanation anywhere.
	s.notifyAccountChange(r, id, "AccountRoleChanged", map[string]any{"roles": strings.Join(in.Roles, ", ")})
	s.audit(r, "user.roles_update", "user", id.String(), nil, in, "success")
	writeJSON(w, 200, map[string]any{"ok": true})
}

// resetAdminUserMFA is the recovery path for a lost authenticator. Without it a
// user who loses their device is locked out permanently, and an offline
// deployment with a single administrator has no way back in at all.
func (s *Server) resetAdminUserMFA(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDPath(w, r, "id")
	if !ok {
		return
	}
	tag, err := s.DB.Exec(r.Context(), `DELETE FROM mfa_factors WHERE user_id=$1`, id)
	if err != nil {
		writeError(w, 500, "mfa_reset_failed", "MFA를 초기화하지 못했습니다.")
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, 404, "mfa_not_found", "이 계정에는 등록된 MFA가 없습니다.")
		return
	}
	// Removing the second factor is exactly the moment to end sessions that may
	// have been opened with it.
	_, _ = s.DB.Exec(r.Context(), `UPDATE sessions SET revoked_at=now() WHERE user_id=$1 AND revoked_at IS NULL`, id)
	// The account holder is the only person who can tell whether this was
	// asked for. Doing it silently is how an operator account that has been
	// taken over removes a target's second factor without anyone noticing.
	s.notifyAccountChange(r, id, "AccountMFAReset", nil)
	s.audit(r, "mfa.admin_reset", "user", id.String(), map[string]any{"factors": tag.RowsAffected()}, map[string]any{"totp_enabled": false}, "success")
	writeJSON(w, 200, map[string]any{"ok": true, "removed_factors": tag.RowsAffected()})
}

// getAdminUser is the case file for one person. Investigating a complaint used
// to mean cross referencing five screens by hand — the user list for who they
// are, the order list for what they bought, the dispute and report queues for
// what went wrong, and the audit log for what they did — with no screen that
// put a person together. Every figure here is counted from records, and the
// operator's own actions on this account are the last thing on it.
func (s *Server) getAdminUser(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDPath(w, r, "id")
	if !ok {
		return
	}
	var raw []byte
	err := s.DB.QueryRow(r.Context(), `SELECT jsonb_build_object(
			'id',u.id,'username',u.username,'email',u.email,'display_name',u.display_name,
			'status',u.status,'created_at',u.created_at,'last_login_at',u.last_login_at,
			'roles',COALESCE((SELECT array_agg(ur.role_code ORDER BY ur.role_code) FROM user_roles ur WHERE ur.user_id=u.id),ARRAY[]::text[]),
			'mfa_enabled',EXISTS(SELECT 1 FROM mfa_factors m WHERE m.user_id=u.id AND m.enabled),
			'active_sessions',(SELECT count(*) FROM sessions se WHERE se.user_id=u.id AND se.revoked_at IS NULL AND se.expires_at>now()),
			'active_api_keys',(SELECT count(*) FROM api_keys k WHERE k.user_id=u.id AND k.revoked_at IS NULL),
			'linked_identities',(SELECT count(*) FROM external_identities i WHERE i.user_id=u.id),
			'seller',CASE WHEN sp.user_id IS NULL THEN NULL ELSE jsonb_build_object(
				'headline',sp.headline,'level',sp.level,'score',sp.score,'rating',sp.rating,'rating_count',sp.rating_count,
				'capacity',sp.capacity,'published_talents',(SELECT count(*) FROM talents t WHERE t.seller_id=u.id AND t.status='published')) END,
			'orders',jsonb_build_object(
				'bought',(SELECT count(*) FROM orders o WHERE o.buyer_id=u.id),
				'sold',(SELECT count(*) FROM orders o WHERE o.seller_id=u.id),
				'completed',(SELECT count(*) FROM orders o WHERE (o.buyer_id=u.id OR o.seller_id=u.id) AND o.state='COMPLETED'),
				'cancelled',(SELECT count(*) FROM orders o WHERE (o.buyer_id=u.id OR o.seller_id=u.id) AND o.state IN ('CANCELLED','REFUNDED')),
				'live',(SELECT count(*) FROM orders o WHERE (o.buyer_id=u.id OR o.seller_id=u.id) AND o.state NOT IN ('COMPLETED','CANCELLED','REFUNDED'))),
			'money',jsonb_build_object(
				'paid',(SELECT COALESCE(sum(pm.amount),0) FROM payments pm JOIN orders o ON o.id=pm.order_id WHERE o.buyer_id=u.id AND pm.state='captured'),
				'settled',(SELECT COALESCE(sum(st.net_amount),0) FROM settlements st WHERE st.seller_id=u.id AND st.state='completed'),
				'pending_settlement',(SELECT COALESCE(sum(st.net_amount),0) FROM settlements st WHERE st.seller_id=u.id AND st.state IN ('scheduled','confirmed'))),
			'trouble',jsonb_build_object(
				'disputes_opened',(SELECT count(*) FROM disputes d WHERE d.opened_by=u.id),
				'disputes_against',(SELECT count(*) FROM disputes d JOIN orders o ON o.id=d.order_id WHERE (o.buyer_id=u.id OR o.seller_id=u.id) AND d.opened_by<>u.id),
				'reports_filed',(SELECT count(*) FROM reports rp WHERE rp.reporter_id=u.id),
				'reports_against',(SELECT count(*) FROM reports rp WHERE rp.resource_type='user' AND rp.resource_id=u.id)),
			'organizations',COALESCE((SELECT jsonb_agg(jsonb_build_object('id',og.id,'name',og.name,'role',ou.role) ORDER BY og.name)
				FROM organization_users ou JOIN organizations og ON og.id=ou.organization_id WHERE ou.user_id=u.id),'[]'::jsonb),
			'recent_actions',COALESCE((SELECT jsonb_agg(entry ORDER BY entry->>'occurred_at' DESC) FROM (
				SELECT jsonb_build_object('occurred_at',al.occurred_at,'action',al.action,'resource_type',al.resource_type,'result',al.result) AS entry
				FROM audit_logs al WHERE al.actor_user_id=u.id ORDER BY al.occurred_at DESC LIMIT 10) recent),'[]'::jsonb)
		) FROM users u LEFT JOIN seller_profiles sp ON sp.user_id=u.id WHERE u.id=$1`, id).Scan(&raw)
	if err == pgx.ErrNoRows {
		writeError(w, 404, "user_not_found", "사용자를 찾을 수 없습니다.")
		return
	}
	if err != nil {
		// Reporting a broken query as "not found" sends an operator looking for
		// a deleted account that is sitting right there.
		s.Logger.Error("admin user detail failed", "error", err.Error(), "user", id.String())
		writeError(w, 500, "query_failed", "사용자 정보를 조회하지 못했습니다.")
		return
	}
	var payload any
	_ = json.Unmarshal(raw, &payload)
	writeJSON(w, 200, payload)
}

// notifyAccountChange tells the account holder that an operator changed
// something about their account. It is best effort on purpose: the change has
// already happened and refusing it because the notice failed would be worse
// than a missing notice.
func (s *Server) notifyAccountChange(r *http.Request, user uuid.UUID, event string, payload map[string]any) {
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		return
	}
	defer tx.Rollback(r.Context()) //nolint:errcheck
	if emitEvent(r.Context(), tx, "user", user, event, payload) != nil {
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		s.Logger.Warn("계정 변경 알림을 남기지 못했습니다", "error", err.Error(), "event", event, "user", user.String())
	}
}
