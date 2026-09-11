package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// linkDecision is what the callback should do with a provider identity that is
// not yet linked to anyone.
type linkDecision int

const (
	linkCreateNewAccount linkDecision = iota
	linkToExistingAccount
	linkRefused
)

// existingAccount describes the local account an incoming email matches.
type existingAccount struct {
	Found       bool
	HasPassword bool
	IsAdmin     bool
}

// decideAccountLink is the fix for a takeover route: matching on the email
// claim alone let anyone who could get a provider to assert a victim's address
// sign straight into that account. Linking now requires the provider to say the
// address is verified, and never happens silently for an account that has its
// own password or administrative access — those owners link deliberately from
// their account settings.
func decideAccountLink(account existingAccount, emailVerified, linkByVerifiedEmail bool) (linkDecision, string) {
	if !account.Found {
		return linkCreateNewAccount, ""
	}
	if !linkByVerifiedEmail {
		return linkRefused, "이 이메일은 이미 다른 계정에서 사용 중입니다. 로그인한 뒤 개인화 페이지에서 이 제공자를 연결해 주세요."
	}
	if !emailVerified {
		return linkRefused, "인증 제공자가 이메일 확인 여부를 알려주지 않았습니다. 로그인한 뒤 개인화 페이지에서 이 제공자를 연결해 주세요."
	}
	if account.HasPassword || account.IsAdmin {
		return linkRefused, "이 이메일은 이미 비밀번호가 설정된 계정에서 사용 중입니다. 로그인한 뒤 개인화 페이지에서 이 제공자를 연결해 주세요."
	}
	return linkToExistingAccount, ""
}

func lookupAccountByEmail(ctx context.Context, tx pgx.Tx, email string) (uuid.UUID, existingAccount, error) {
	var id uuid.UUID
	var account existingAccount
	err := tx.QueryRow(ctx, `SELECT id,password_hash IS NOT NULL AND password_hash<>'',
		EXISTS(SELECT 1 FROM user_roles ur JOIN role_permissions rp ON rp.role_code=ur.role_code WHERE ur.user_id=users.id AND rp.permission_code='admin.access')
		FROM users WHERE lower(email)=lower($1) AND status='active'`, email).Scan(&id, &account.HasPassword, &account.IsAdmin)
	if err == pgx.ErrNoRows {
		return uuid.Nil, account, nil
	}
	if err != nil {
		return uuid.Nil, account, err
	}
	account.Found = true
	return id, account, nil
}

func (s *Server) listMyIdentities(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	rows, err := s.DB.Query(r.Context(), `SELECT e.id,a.slug,a.name,e.subject,e.created_at,e.last_login_at
		FROM external_identities e JOIN auth_providers a ON a.id=e.provider_id WHERE e.user_id=$1 ORDER BY e.created_at`, p.UserID)
	if err != nil {
		writeError(w, 500, "query_failed", "연결된 계정을 조회하지 못했습니다.")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id uuid.UUID
		var slug, name, subject string
		var created, lastLogin time.Time
		if rows.Scan(&id, &slug, &name, &subject, &created, &lastLogin) == nil {
			items = append(items, map[string]any{"id": id, "provider_slug": slug, "provider_name": name,
				"subject": subject, "created_at": created, "last_login_at": lastLogin})
		}
	}
	var hasPassword bool
	_ = s.DB.QueryRow(r.Context(), `SELECT password_hash IS NOT NULL AND password_hash<>'' FROM users WHERE id=$1`, p.UserID).Scan(&hasPassword)
	writeJSON(w, 200, map[string]any{"items": items, "has_password": hasPassword})
}

// unlinkIdentity refuses to remove the only way an account can sign in.
func (s *Server) unlinkIdentity(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	id, ok := parseUUIDPath(w, r, "id")
	if !ok {
		return
	}
	var hasPassword bool
	var identities int
	if err := s.DB.QueryRow(r.Context(), `SELECT (SELECT password_hash IS NOT NULL AND password_hash<>'' FROM users WHERE id=$1),
		(SELECT count(*) FROM external_identities WHERE user_id=$1)`, p.UserID).Scan(&hasPassword, &identities); err != nil {
		writeError(w, 500, "query_failed", "계정 상태를 확인하지 못했습니다.")
		return
	}
	if !hasPassword && identities <= 1 {
		writeError(w, 409, "last_sign_in_method", "마지막 로그인 수단은 해제할 수 없습니다. 먼저 비밀번호를 설정해 주세요.")
		return
	}
	tag, err := s.DB.Exec(r.Context(), `DELETE FROM external_identities WHERE id=$1 AND user_id=$2`, id, p.UserID)
	if err != nil || tag.RowsAffected() == 0 {
		writeError(w, 404, "identity_not_found", "연결된 계정을 찾을 수 없습니다.")
		return
	}
	s.audit(r, "identity.unlink", "user", p.UserID.String(), nil, map[string]any{"identity_id": id}, "success")
	w.WriteHeader(http.StatusNoContent)
}
