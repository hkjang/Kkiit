package httpapi

import (
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/hkjang/Kkiit/internal/cryptox"
)

type apiKeyInput struct {
	Name         string     `json:"name"`
	Scopes       []string   `json:"scopes"`
	AllowedCIDRs []string   `json:"allowed_cidrs"`
	RateLimit    int        `json:"rate_limit_per_minute"`
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
}

func (s *Server) listMyAPIKeys(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	rows, err := s.DB.Query(r.Context(), `SELECT id,name,prefix,scopes,allowed_cidrs::text[],rate_limit_per_minute,expires_at,last_used_at,rotated_from,revoked_at,created_at FROM api_keys WHERE user_id=$1 ORDER BY created_at DESC`, p.UserID)
	if err != nil {
		writeError(w, 500, "query_failed", "API 키를 조회하지 못했습니다.")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id uuid.UUID
		var name, prefix string
		var scopes, cidrs []string
		var rate int
		var expires, lastUsed, revoked *time.Time
		var rotated *uuid.UUID
		var created time.Time
		if rows.Scan(&id, &name, &prefix, &scopes, &cidrs, &rate, &expires, &lastUsed, &rotated, &revoked, &created) == nil {
			items = append(items, map[string]any{"id": id, "name": name, "prefix": prefix, "scopes": scopes, "allowed_cidrs": cidrs, "rate_limit_per_minute": rate, "expires_at": expires, "last_used_at": lastUsed, "rotated_from": rotated, "revoked_at": revoked, "created_at": created})
		}
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

func (s *Server) validateKeyInput(r *http.Request, p Principal, in *apiKeyInput) bool {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 100 {
		return false
	}
	if in.RateLimit == 0 {
		in.RateLimit = 60
	}
	if in.RateLimit < 1 || in.RateLimit > 10000 {
		return false
	}
	allowed := map[string]bool{}
	for _, permission := range p.Permissions {
		allowed[permission] = true
	}
	for _, scope := range in.Scopes {
		if !allowed[scope] {
			return false
		}
	}
	if len(in.Scopes) == 0 && allowed["mcp.use"] {
		in.Scopes = []string{"mcp.use"}
	}
	return true
}

func generateAPIKey() (string, string, error) {
	prefix, err := cryptox.RandomToken(6)
	if err != nil {
		return "", "", err
	}
	secret, err := cryptox.RandomToken(32)
	if err != nil {
		return "", "", err
	}
	return prefix, "kkiit_" + prefix + "_" + secret, nil
}

func (s *Server) createMyAPIKey(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	var in apiKeyInput
	if !decodeJSON(w, r, &in) {
		return
	}
	if !s.validateKeyInput(r, p, &in) {
		writeError(w, 400, "invalid_key_policy", "키 이름, 권한, IP 제한 또는 만료일을 확인해 주세요.")
		return
	}
	prefix, token, err := generateAPIKey()
	if err != nil {
		writeError(w, 500, "key_generation_failed", "API 키를 만들지 못했습니다.")
		return
	}
	id := uuid.New()
	_, err = s.DB.Exec(r.Context(), `INSERT INTO api_keys(id,user_id,name,prefix,secret_hash,scopes,allowed_cidrs,rate_limit_per_minute,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7::cidr[],$8,$9)`, id, p.UserID, in.Name, prefix, cryptox.Digest(token), in.Scopes, in.AllowedCIDRs, in.RateLimit, nullableTime(in.ExpiresAt))
	if err != nil {
		writeError(w, 400, "invalid_key_policy", "API 키 정책을 저장하지 못했습니다.")
		return
	}
	s.audit(r, "api_key.create", "api_key", id.String(), nil, map[string]any{"name": in.Name, "scopes": in.Scopes}, "success")
	writeJSON(w, 201, map[string]any{"id": id, "prefix": prefix, "secret": token, "warning": "이 키는 다시 표시되지 않습니다."})
}

func (s *Server) rotateMyAPIKey(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	oldID, ok := parseUUIDPath(w, r, "id")
	if !ok {
		return
	}
	tx, err := s.DB.BeginTx(r.Context(), pgx.TxOptions{})
	if err != nil {
		writeError(w, 500, "transaction_failed", "키 회전을 시작하지 못했습니다.")
		return
	}
	defer tx.Rollback(r.Context())
	var in apiKeyInput
	var oldRevoked *time.Time
	err = tx.QueryRow(r.Context(), `SELECT name,scopes,allowed_cidrs::text[],rate_limit_per_minute,expires_at,revoked_at FROM api_keys WHERE id=$1 AND user_id=$2 FOR UPDATE`, oldID, p.UserID).Scan(&in.Name, &in.Scopes, &in.AllowedCIDRs, &in.RateLimit, &in.ExpiresAt, &oldRevoked)
	if err != nil || oldRevoked != nil {
		writeError(w, 404, "active_key_not_found", "회전할 활성 키를 찾을 수 없습니다.")
		return
	}
	prefix, token, err := generateAPIKey()
	if err != nil {
		writeError(w, 500, "key_generation_failed", "API 키를 만들지 못했습니다.")
		return
	}
	newID := uuid.New()
	_, err = tx.Exec(r.Context(), `UPDATE api_keys SET revoked_at=now() WHERE id=$1`, oldID)
	if err == nil {
		_, err = tx.Exec(r.Context(), `INSERT INTO api_keys(id,user_id,name,prefix,secret_hash,scopes,allowed_cidrs,rate_limit_per_minute,expires_at,rotated_from) VALUES($1,$2,$3,$4,$5,$6,$7::cidr[],$8,$9,$10)`, newID, p.UserID, in.Name, prefix, cryptox.Digest(token), in.Scopes, in.AllowedCIDRs, in.RateLimit, nullableTime(in.ExpiresAt), oldID)
	}
	if err != nil {
		writeError(w, 500, "rotation_failed", "API 키를 회전하지 못했습니다.")
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		writeError(w, 500, "rotation_failed", "API 키를 회전하지 못했습니다.")
		return
	}
	s.audit(r, "api_key.rotate", "api_key", newID.String(), map[string]any{"rotated_from": oldID}, map[string]any{"scopes": in.Scopes}, "success")
	writeJSON(w, 201, map[string]any{"id": newID, "prefix": prefix, "secret": token, "rotated_from": oldID, "warning": "이 키는 다시 표시되지 않습니다."})
}

func (s *Server) revokeMyAPIKey(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	id, ok := parseUUIDPath(w, r, "id")
	if !ok {
		return
	}
	tag, err := s.DB.Exec(r.Context(), `UPDATE api_keys SET revoked_at=now() WHERE id=$1 AND user_id=$2 AND revoked_at IS NULL`, id, p.UserID)
	if err != nil || tag.RowsAffected() == 0 {
		writeError(w, 404, "active_key_not_found", "활성 키를 찾을 수 없습니다.")
		return
	}
	s.audit(r, "api_key.revoke", "api_key", id.String(), nil, nil, "success")
	w.WriteHeader(204)
}
