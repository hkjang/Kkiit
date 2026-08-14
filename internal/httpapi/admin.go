package httpapi

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *Server) listSettings(w http.ResponseWriter, r *http.Request) {
	rows, err := s.DB.Query(r.Context(), `SELECT key,value,is_secret,(encrypted_value IS NOT NULL),version,description,updated_at FROM system_settings ORDER BY key`)
	if err != nil {
		writeError(w, 500, "query_failed", "설정을 조회하지 못했습니다.")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var key, description string
		var raw []byte
		var secret, configured bool
		var version int64
		var updated any
		if err := rows.Scan(&key, &raw, &secret, &configured, &version, &description, &updated); err != nil {
			continue
		}
		var value any
		_ = json.Unmarshal(raw, &value)
		items = append(items, map[string]any{"key": key, "value": value, "is_secret": secret, "secret_configured": configured, "version": version, "description": description, "updated_at": updated})
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

func (s *Server) putSetting(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	var input struct {
		Value       any     `json:"value"`
		Secret      *string `json:"secret,omitempty"`
		Version     int64   `json:"version"`
		Description *string `json:"description,omitempty"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if key == "auth.oauth" {
		value, ok := input.Value.(map[string]any)
		if !ok {
			writeError(w, 400, "invalid_value", "OAuth 설정 값을 확인해 주세요.")
			return
		}
		if base, exists := value["callback_base_url"]; exists {
			text, ok := base.(string)
			if !ok {
				writeError(w, 400, "invalid_callback_base_url", "외부 서비스 주소는 URL 문자열이어야 합니다.")
				return
			}
			if strings.TrimSpace(text) != "" {
				normalized, valid := normalizeExternalBaseURL(text)
				if !valid {
					writeError(w, 400, "invalid_callback_base_url", "외부 서비스 주소는 http:// 또는 https://로 시작하는 올바른 URL이어야 합니다.")
					return
				}
				value["callback_base_url"] = normalized
			}
		}
	}
	valueJSON, err := json.Marshal(input.Value)
	if err != nil {
		writeError(w, 400, "invalid_value", "설정 값을 확인해 주세요.")
		return
	}
	var beforeRaw []byte
	var beforeVersion int64
	err = s.DB.QueryRow(r.Context(), `SELECT value,version FROM system_settings WHERE key=$1`, key).Scan(&beforeRaw, &beforeVersion)
	if err != nil && err != pgx.ErrNoRows {
		writeError(w, 500, "query_failed", "설정을 확인하지 못했습니다.")
		return
	}
	if err == nil && input.Version != 0 && input.Version != beforeVersion {
		writeError(w, 409, "setting_conflict", "다른 관리자가 설정을 변경했습니다. 새로고침 후 다시 시도해 주세요.")
		return
	}
	var encrypted any
	isSecret := input.Secret != nil
	if input.Secret != nil {
		cipher, err := s.Box.Encrypt([]byte(*input.Secret), "setting:"+key)
		if err != nil {
			writeError(w, 500, "encryption_failed", "비밀 설정을 암호화하지 못했습니다.")
			return
		}
		encrypted = cipher
	}
	p, _ := principalFrom(r.Context())
	description := ""
	if input.Description != nil {
		description = *input.Description
	}
	if err == pgx.ErrNoRows {
		_, err = s.DB.Exec(r.Context(), `INSERT INTO system_settings(key,value,encrypted_value,is_secret,description,updated_by) VALUES($1,$2,$3,$4,$5,$6)`, key, valueJSON, encrypted, isSecret, description, p.UserID)
	} else if input.Secret != nil {
		_, err = s.DB.Exec(r.Context(), `UPDATE system_settings SET value=$2,encrypted_value=$3,is_secret=true,description=CASE WHEN $4='' THEN description ELSE $4 END,version=version+1,updated_by=$5,updated_at=now() WHERE key=$1`, key, valueJSON, encrypted, description, p.UserID)
	} else {
		_, err = s.DB.Exec(r.Context(), `UPDATE system_settings SET value=$2,description=CASE WHEN $3='' THEN description ELSE $3 END,version=version+1,updated_by=$4,updated_at=now() WHERE key=$1`, key, valueJSON, description, p.UserID)
	}
	if err != nil {
		writeError(w, 500, "update_failed", "설정을 저장하지 못했습니다.")
		return
	}
	var before any
	_ = json.Unmarshal(beforeRaw, &before)
	s.audit(r, "settings.update", "system_setting", key, before, input.Value, "success")
	writeJSON(w, 200, map[string]any{"ok": true})
}

type providerInput struct {
	Slug             string         `json:"slug"`
	Name             string         `json:"name"`
	Preset           string         `json:"preset"`
	ProviderType     string         `json:"provider_type"`
	Enabled          bool           `json:"enabled"`
	IssuerURL        string         `json:"issuer_url"`
	AuthorizationURL string         `json:"authorization_url"`
	TokenURL         string         `json:"token_url"`
	UserinfoURL      string         `json:"userinfo_url"`
	ClientID         string         `json:"client_id"`
	ClientSecret     *string        `json:"client_secret,omitempty"`
	Scopes           []string       `json:"scopes"`
	ClaimMapping     map[string]any `json:"claim_mapping"`
	Options          map[string]any `json:"options"`
}

func applyProviderPreset(in *providerInput) {
	if in.ProviderType == "" {
		in.ProviderType = "oidc"
	}
	if len(in.Scopes) == 0 {
		in.Scopes = []string{"openid", "profile", "email"}
	}
	if in.ClaimMapping == nil {
		in.ClaimMapping = map[string]any{"subject": "sub", "email": "email", "name": "name"}
	}
	switch in.Preset {
	case "google":
		in.ProviderType = "oidc"
		if in.IssuerURL == "" {
			in.IssuerURL = "https://accounts.google.com"
		}
	case "apple":
		in.ProviderType = "oidc"
		if in.IssuerURL == "" {
			in.IssuerURL = "https://appleid.apple.com"
		}
		in.Scopes = []string{"openid", "name", "email"}
	case "naver":
		in.ProviderType = "oauth2"
		in.AuthorizationURL = "https://nid.naver.com/oauth2.0/authorize"
		in.TokenURL = "https://nid.naver.com/oauth2.0/token"
		in.UserinfoURL = "https://openapi.naver.com/v1/nid/me"
		in.Scopes = []string{}
		in.ClaimMapping = map[string]any{"subject": "response.id", "email": "response.email", "name": "response.name"}
	case "kakao":
		in.ProviderType = "oauth2"
		in.AuthorizationURL = "https://kauth.kakao.com/oauth/authorize"
		in.TokenURL = "https://kauth.kakao.com/oauth/token"
		in.UserinfoURL = "https://kapi.kakao.com/v2/user/me"
		in.Scopes = []string{"profile_nickname", "account_email"}
		in.ClaimMapping = map[string]any{"subject": "id", "email": "kakao_account.email", "name": "properties.nickname"}
	case "keycloak":
		in.ProviderType = "oidc"
	}
}

func validateProvider(in *providerInput) bool {
	in.Slug = strings.ToLower(strings.TrimSpace(in.Slug))
	in.Name = strings.TrimSpace(in.Name)
	in.ClientID = strings.TrimSpace(in.ClientID)
	in.IssuerURL = strings.TrimRight(strings.TrimSpace(in.IssuerURL), "/")
	in.AuthorizationURL = strings.TrimSpace(in.AuthorizationURL)
	in.TokenURL = strings.TrimSpace(in.TokenURL)
	in.UserinfoURL = strings.TrimSpace(in.UserinfoURL)
	if !providerSlugPattern.MatchString(in.Slug) || in.Name == "" || in.ClientID == "" {
		return false
	}
	if in.ProviderType == "oidc" {
		_, valid := normalizeExternalBaseURL(in.IssuerURL)
		return valid
	}
	_, authorizationValid := normalizeExternalBaseURL(in.AuthorizationURL)
	_, tokenValid := normalizeExternalBaseURL(in.TokenURL)
	_, userinfoValid := normalizeExternalBaseURL(in.UserinfoURL)
	return authorizationValid && tokenValid && userinfoValid
}

var providerSlugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,49}$`)

func (s *Server) listAuthProviders(w http.ResponseWriter, r *http.Request) {
	rows, err := s.DB.Query(r.Context(), `SELECT id,slug,provider_type,preset,name,enabled,issuer_url,authorization_url,token_url,userinfo_url,client_id,(client_secret_encrypted IS NOT NULL),scopes,claim_mapping,options,created_at,updated_at FROM auth_providers ORDER BY name`)
	if err != nil {
		writeError(w, 500, "query_failed", "인증 제공자를 조회하지 못했습니다.")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id uuid.UUID
		var slug, typ, preset, name, client string
		var enabled, secret bool
		var issuer, authURL, tokenURL, userinfo *string
		var scopes []string
		var mapping, options []byte
		var created, updated any
		if rows.Scan(&id, &slug, &typ, &preset, &name, &enabled, &issuer, &authURL, &tokenURL, &userinfo, &client, &secret, &scopes, &mapping, &options, &created, &updated) != nil {
			continue
		}
		var m, o any
		_ = json.Unmarshal(mapping, &m)
		_ = json.Unmarshal(options, &o)
		items = append(items, map[string]any{"id": id, "slug": slug, "provider_type": typ, "preset": preset, "name": name, "enabled": enabled, "issuer_url": issuer, "authorization_url": authURL, "token_url": tokenURL, "userinfo_url": userinfo, "client_id": client, "secret_configured": secret, "scopes": scopes, "claim_mapping": m, "options": o, "created_at": created, "updated_at": updated})
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

func (s *Server) createAuthProvider(w http.ResponseWriter, r *http.Request) {
	var in providerInput
	if !decodeJSON(w, r, &in) {
		return
	}
	applyProviderPreset(&in)
	if !validateProvider(&in) {
		writeError(w, 400, "invalid_provider", "인증 제공자 필수 설정을 확인해 주세요.")
		return
	}
	id := uuid.New()
	var encrypted any
	if in.ClientSecret != nil {
		cipher, err := s.Box.Encrypt([]byte(*in.ClientSecret), "auth-provider:"+id.String())
		if err != nil {
			writeError(w, 500, "encryption_failed", "비밀값을 암호화하지 못했습니다.")
			return
		}
		encrypted = cipher
	}
	p, _ := principalFrom(r.Context())
	_, err := s.DB.Exec(r.Context(), `INSERT INTO auth_providers(id,slug,provider_type,preset,name,enabled,issuer_url,authorization_url,token_url,userinfo_url,client_id,client_secret_encrypted,scopes,claim_mapping,options,created_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`, id, in.Slug, in.ProviderType, in.Preset, in.Name, in.Enabled, nullableString(in.IssuerURL), nullableString(in.AuthorizationURL), nullableString(in.TokenURL), nullableString(in.UserinfoURL), in.ClientID, encrypted, in.Scopes, in.ClaimMapping, in.Options, p.UserID)
	if err != nil {
		writeError(w, 409, "provider_conflict", "같은 식별자의 인증 제공자가 있거나 설정이 올바르지 않습니다.")
		return
	}
	s.audit(r, "auth_provider.create", "auth_provider", id.String(), nil, map[string]any{"slug": in.Slug, "enabled": in.Enabled}, "success")
	writeJSON(w, 201, map[string]any{"id": id})
}

func (s *Server) updateAuthProvider(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDPath(w, r, "id")
	if !ok {
		return
	}
	var in providerInput
	if !decodeJSON(w, r, &in) {
		return
	}
	applyProviderPreset(&in)
	if !validateProvider(&in) {
		writeError(w, 400, "invalid_provider", "인증 제공자 필수 설정을 확인해 주세요.")
		return
	}
	var encrypted any
	if in.ClientSecret != nil {
		cipher, err := s.Box.Encrypt([]byte(*in.ClientSecret), "auth-provider:"+id.String())
		if err != nil {
			writeError(w, 500, "encryption_failed", "비밀값을 암호화하지 못했습니다.")
			return
		}
		encrypted = cipher
	}
	tag, err := s.DB.Exec(r.Context(), `UPDATE auth_providers SET slug=$2,provider_type=$3,preset=$4,name=$5,enabled=$6,issuer_url=$7,authorization_url=$8,token_url=$9,userinfo_url=$10,client_id=$11,client_secret_encrypted=CASE WHEN $12::bytea IS NULL THEN client_secret_encrypted ELSE $12 END,scopes=$13,claim_mapping=$14,options=$15,updated_at=now() WHERE id=$1`, id, in.Slug, in.ProviderType, in.Preset, in.Name, in.Enabled, nullableString(in.IssuerURL), nullableString(in.AuthorizationURL), nullableString(in.TokenURL), nullableString(in.UserinfoURL), in.ClientID, encrypted, in.Scopes, in.ClaimMapping, in.Options)
	if err != nil {
		writeError(w, 409, "provider_conflict", "인증 제공자 설정을 저장하지 못했습니다.")
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, 404, "not_found", "인증 제공자를 찾을 수 없습니다.")
		return
	}
	s.audit(r, "auth_provider.update", "auth_provider", id.String(), nil, map[string]any{"slug": in.Slug, "enabled": in.Enabled}, "success")
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) deleteAuthProvider(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDPath(w, r, "id")
	if !ok {
		return
	}
	var identityCount int
	if err := s.DB.QueryRow(r.Context(), `SELECT count(*) FROM external_identities WHERE provider_id=$1`, id).Scan(&identityCount); err != nil {
		writeError(w, 500, "query_failed", "연결된 계정을 확인하지 못했습니다.")
		return
	}
	if identityCount > 0 {
		writeError(w, 409, "provider_in_use", "연결된 계정이 있어 삭제할 수 없습니다. 먼저 비활성화해 주세요.")
		return
	}
	tag, err := s.DB.Exec(r.Context(), `DELETE FROM auth_providers WHERE id=$1`, id)
	if err != nil {
		writeError(w, 409, "provider_in_use", "연결된 계정이 있어 삭제할 수 없습니다. 먼저 비활성화해 주세요.")
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, 404, "not_found", "인증 제공자를 찾을 수 없습니다.")
		return
	}
	s.audit(r, "auth_provider.delete", "auth_provider", id.String(), nil, nil, "success")
	w.WriteHeader(204)
}

func (s *Server) listRoles(w http.ResponseWriter, r *http.Request) {
	rows, err := s.DB.Query(r.Context(), `SELECT r.code,r.name,r.description,r.system_role,COALESCE(array_agg(rp.permission_code ORDER BY rp.permission_code) FILTER (WHERE rp.permission_code IS NOT NULL),ARRAY[]::text[]) FROM roles r LEFT JOIN role_permissions rp ON rp.role_code=r.code GROUP BY r.code ORDER BY r.code`)
	if err != nil {
		writeError(w, 500, "query_failed", "역할을 조회하지 못했습니다.")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var code, name, description string
		var system bool
		var permissions []string
		if rows.Scan(&code, &name, &description, &system, &permissions) == nil {
			items = append(items, map[string]any{"code": code, "name": name, "description": description, "system_role": system, "permissions": permissions})
		}
	}
	var catalog []map[string]any
	prows, _ := s.DB.Query(r.Context(), `SELECT code,name,description FROM permissions ORDER BY code`)
	if prows != nil {
		defer prows.Close()
		catalog = make([]map[string]any, 0)
		for prows.Next() {
			var c, n, d string
			if prows.Scan(&c, &n, &d) == nil {
				catalog = append(catalog, map[string]any{"code": c, "name": n, "description": d})
			}
		}
	}
	writeJSON(w, 200, map[string]any{"items": items, "permissions": catalog})
}

func (s *Server) updateRolePermissions(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	var in struct {
		Permissions []string `json:"permissions"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		writeError(w, 500, "transaction_failed", "권한 변경을 시작하지 못했습니다.")
		return
	}
	defer tx.Rollback(r.Context())
	var exists bool
	if tx.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM roles WHERE code=$1)`, code).Scan(&exists) != nil || !exists {
		writeError(w, 404, "not_found", "역할을 찾을 수 없습니다.")
		return
	}
	if _, err = tx.Exec(r.Context(), `DELETE FROM role_permissions WHERE role_code=$1`, code); err == nil {
		for _, permission := range in.Permissions {
			if _, err = tx.Exec(r.Context(), `INSERT INTO role_permissions(role_code,permission_code) VALUES($1,$2)`, code, permission); err != nil {
				break
			}
		}
	}
	if err != nil {
		writeError(w, 400, "invalid_permission", "권한 목록을 확인해 주세요.")
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		writeError(w, 500, "update_failed", "권한을 저장하지 못했습니다.")
		return
	}
	s.audit(r, "role.permissions_update", "role", code, nil, in, "success")
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) listAuditLogs(w http.ResponseWriter, r *http.Request) {
	limit := 100
	rows, err := s.DB.Query(r.Context(), `SELECT id,occurred_at,actor_user_id,actor_roles,ip_address::text,action,resource_type,resource_id,before_data,after_data,request_id,result FROM audit_logs ORDER BY occurred_at DESC LIMIT $1`, limit)
	if err != nil {
		writeError(w, 500, "query_failed", "감사 로그를 조회하지 못했습니다.")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id uuid.UUID
		var occurred any
		var actor *uuid.UUID
		var roles []string
		var ip *string
		var action, typ, requestID, result string
		var resource *string
		var before, after []byte
		if rows.Scan(&id, &occurred, &actor, &roles, &ip, &action, &typ, &resource, &before, &after, &requestID, &result) != nil {
			continue
		}
		var b, a any
		_ = json.Unmarshal(before, &b)
		_ = json.Unmarshal(after, &a)
		items = append(items, map[string]any{"id": id, "occurred_at": occurred, "actor_user_id": actor, "actor_roles": roles, "ip": ip, "action": action, "resource_type": typ, "resource_id": resource, "before": b, "after": a, "request_id": requestID, "result": result})
	}
	writeJSON(w, 200, map[string]any{"items": items})
}
