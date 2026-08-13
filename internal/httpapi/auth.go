package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"golang.org/x/oauth2"

	"github.com/hkjang/Kkiit/internal/cryptox"
	"github.com/hkjang/Kkiit/internal/password"
)

type authProvider struct {
	ID                    uuid.UUID
	Slug                  string
	ProviderType          string
	Preset                string
	Name                  string
	Enabled               bool
	IssuerURL             *string
	AuthorizationURL      *string
	TokenURL              *string
	UserinfoURL           *string
	ClientID              string
	ClientSecretEncrypted []byte
	Scopes                []string
	ClaimMapping          map[string]any
	Options               map[string]any
}

func (s *Server) listEnabledProviders(w http.ResponseWriter, r *http.Request) {
	rows, err := s.DB.Query(r.Context(), `SELECT slug,name,preset FROM auth_providers WHERE enabled ORDER BY name`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query_failed", "인증 제공자를 조회하지 못했습니다.")
		return
	}
	defer rows.Close()
	providers := make([]map[string]any, 0)
	for rows.Next() {
		var slug, name, preset string
		if err := rows.Scan(&slug, &name, &preset); err == nil {
			providers = append(providers, map[string]any{"slug": slug, "name": name, "preset": preset, "login_url": "/api/v1/auth/oauth/" + slug + "/start"})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": providers})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
		MFACode  string `json:"mfa_code"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	security, err := s.settingObject(r, "auth.security")
	if err == nil {
		if allowed, ok := security["allow_local_login"].(bool); ok && !allowed {
			writeError(w, http.StatusForbidden, "local_login_disabled", "로컬 로그인이 비활성화되어 있습니다.")
			return
		}
	}
	var userID uuid.UUID
	var hash string
	err = s.DB.QueryRow(r.Context(), `SELECT id,password_hash FROM users WHERE (lower(username)=lower($1) OR lower(email)=lower($1)) AND status='active'`, strings.TrimSpace(input.Username)).Scan(&userID, &hash)
	if err != nil || !password.Verify(hash, input.Password) {
		time.Sleep(150 * time.Millisecond)
		s.audit(r, "auth.login", "user", "", nil, nil, "failure")
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "아이디 또는 비밀번호가 올바르지 않습니다.")
		return
	}
	mfaRequired, mfaValid, mfaErr := s.verifyLoginTOTP(r.Context(), userID, input.MFACode)
	if mfaErr != nil {
		writeError(w, http.StatusInternalServerError, "mfa_failed", "MFA 설정을 확인하지 못했습니다.")
		return
	}
	if mfaRequired && strings.TrimSpace(input.MFACode) == "" {
		writeError(w, http.StatusUnauthorized, "mfa_required", "인증 앱의 6자리 코드를 입력해 주세요.")
		return
	}
	if mfaRequired && !mfaValid {
		s.audit(r, "auth.mfa", "user", userID.String(), nil, nil, "failure")
		writeError(w, http.StatusUnauthorized, "invalid_mfa_code", "인증 코드가 올바르지 않습니다.")
		return
	}
	if !mfaRequired {
		security, settingErr := s.settingObject(r, "auth.security")
		adminMFA, _ := security["mfa_admin_required"].(bool)
		var admin bool
		if settingErr == nil && adminMFA {
			_ = s.DB.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM user_roles ur JOIN role_permissions rp ON rp.role_code=ur.role_code WHERE ur.user_id=$1 AND rp.permission_code='admin.access')`, userID).Scan(&admin)
		}
		if admin {
			writeError(w, http.StatusForbidden, "mfa_enrollment_required", "관리자 MFA 정책이 활성화되어 있습니다. 정책 활성화 전에 이 계정에 TOTP를 등록해야 합니다.")
			return
		}
	}
	if err := s.issueSession(w, r, userID); err != nil {
		writeError(w, http.StatusInternalServerError, "session_failed", "세션을 만들지 못했습니다.")
		return
	}
	_, _ = s.DB.Exec(r.Context(), `UPDATE users SET last_login_at=now() WHERE id=$1`, userID)
	auditRequest := r.WithContext(context.WithValue(r.Context(), principalKey, Principal{UserID: userID, Roles: []string{}}))
	s.audit(auditRequest, "auth.login", "user", userID.String(), nil, nil, "success")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	general, err := s.settingObject(r, "service.general")
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "registration_unavailable", "가입 정책을 확인하지 못했습니다.")
		return
	}
	if enabled, _ := general["public_registration"].(bool); !enabled {
		writeError(w, http.StatusForbidden, "registration_disabled", "현재 신규 가입을 받지 않습니다.")
		return
	}
	var input struct {
		Username    string `json:"username"`
		Email       string `json:"email"`
		DisplayName string `json:"display_name"`
		Password    string `json:"password"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Username = strings.ToLower(strings.TrimSpace(input.Username))
	input.Email = strings.ToLower(strings.TrimSpace(input.Email))
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	if len(input.Username) < 3 || len(input.Username) > 50 || usernameClean.ReplaceAllString(input.Username, "") != input.Username || len(input.DisplayName) < 2 || len(input.DisplayName) > 100 || !strings.Contains(input.Email, "@") {
		writeError(w, http.StatusBadRequest, "invalid_registration", "아이디, 이메일과 표시 이름을 확인해 주세요.")
		return
	}
	hash, err := password.Hash(input.Password)
	if err != nil {
		writeError(w, http.StatusBadRequest, "weak_password", "비밀번호는 12자 이상이어야 합니다.")
		return
	}
	userID := uuid.New()
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		writeError(w, 500, "registration_failed", "가입을 시작하지 못했습니다.")
		return
	}
	defer tx.Rollback(r.Context()) //nolint:errcheck
	_, err = tx.Exec(r.Context(), `INSERT INTO users(id,username,email,password_hash,display_name) VALUES($1,$2,$3,$4,$5)`, userID, input.Username, input.Email, hash, input.DisplayName)
	if err == nil {
		_, err = tx.Exec(r.Context(), `INSERT INTO user_roles(user_id,role_code) VALUES($1,'buyer')`, userID)
	}
	if err != nil {
		writeError(w, http.StatusConflict, "account_exists", "이미 사용 중인 아이디 또는 이메일입니다.")
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		writeError(w, 500, "registration_failed", "가입을 완료하지 못했습니다.")
		return
	}
	if err := s.issueSession(w, r, userID); err != nil {
		writeError(w, 500, "session_failed", "가입했지만 로그인 세션을 만들지 못했습니다.")
		return
	}
	auditRequest := r.WithContext(context.WithValue(r.Context(), principalKey, Principal{UserID: userID, Roles: []string{"buyer"}}))
	s.audit(auditRequest, "auth.register", "user", userID.String(), nil, map[string]any{"username": input.Username}, "success")
	writeJSON(w, http.StatusCreated, map[string]any{"id": userID})
}

func (s *Server) issueSession(w http.ResponseWriter, r *http.Request, userID uuid.UUID) error {
	token, err := cryptox.RandomToken(48)
	if err != nil {
		return err
	}
	ttl := 12 * time.Hour
	secure := false
	if security, err := s.settingObject(r, "auth.security"); err == nil {
		if hours, ok := security["session_ttl_hours"].(float64); ok && hours >= 1 && hours <= 24*30 {
			ttl = time.Duration(hours * float64(time.Hour))
		}
		secure, _ = security["cookie_secure"].(bool)
	}
	_, err = s.DB.Exec(r.Context(), `INSERT INTO sessions(id,user_id,token_hash,ip_address,user_agent,expires_at) VALUES($1,$2,$3,$4,$5,$6)`,
		uuid.New(), userID, cryptox.Digest(token), clientIP(r), r.UserAgent(), time.Now().Add(ttl))
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{Name: "kkiit_session", Value: token, Path: "/", HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode, MaxAge: int(ttl.Seconds())})
	return nil
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	if p.SessionID != nil {
		_, _ = s.DB.Exec(r.Context(), `UPDATE sessions SET revoked_at=now() WHERE id=$1`, *p.SessionID)
	}
	http.SetCookie(w, &http.Cookie{Name: "kkiit_session", Value: "", Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: -1})
	s.audit(r, "auth.logout", "session", fmt.Sprint(p.SessionID), nil, nil, "success")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) updateMe(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	var input struct {
		DisplayName string         `json:"display_name"`
		Locale      string         `json:"locale"`
		Timezone    string         `json:"timezone"`
		Profile     map[string]any `json:"profile"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	if input.DisplayName == "" || len(input.DisplayName) > 100 {
		writeError(w, http.StatusBadRequest, "invalid_display_name", "표시 이름을 확인해 주세요.")
		return
	}
	_, err := s.DB.Exec(r.Context(), `UPDATE users SET display_name=$2,locale=$3,timezone=$4,profile=$5,updated_at=now() WHERE id=$1`, p.UserID, input.DisplayName, input.Locale, input.Timezone, input.Profile)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "update_failed", "프로필을 저장하지 못했습니다.")
		return
	}
	s.audit(r, "profile.update", "user", p.UserID.String(), nil, input, "success")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) oauthStart(w http.ResponseWriter, r *http.Request) {
	provider, err := s.loadAuthProvider(r.Context(), r.PathValue("slug"), true)
	if err != nil {
		writeError(w, http.StatusNotFound, "provider_not_found", "활성화된 인증 제공자를 찾을 수 없습니다.")
		return
	}
	config, _, err := s.oauthConfig(r.Context(), r, provider, "")
	if err != nil {
		s.Logger.Error("oauth provider setup failed", "provider", provider.Slug, "error", err)
		writeError(w, http.StatusBadGateway, "provider_unavailable", "인증 제공자 설정 또는 연결을 확인해 주세요.")
		return
	}
	state, _ := cryptox.RandomToken(32)
	verifier, _ := cryptox.RandomToken(48)
	challengeHash := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(challengeHash[:])
	encrypted, err := s.Box.Encrypt([]byte(verifier), "oauth:"+state)
	if err != nil {
		writeError(w, 500, "encryption_failed", "인증 요청을 만들지 못했습니다.")
		return
	}
	_, err = s.DB.Exec(r.Context(), `INSERT INTO oauth_states(state_hash,provider_id,verifier_encrypted,redirect_uri,expires_at) VALUES($1,$2,$3,$4,now()+interval '10 minutes')`, cryptox.Digest(state), provider.ID, encrypted, config.RedirectURL)
	if err != nil {
		writeError(w, 500, "state_failed", "인증 요청을 저장하지 못했습니다.")
		return
	}
	http.Redirect(w, r, config.AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.SetAuthURLParam("code_challenge", challenge), oauth2.SetAuthURLParam("code_challenge_method", "S256")), http.StatusFound)
}

func (s *Server) oauthCallback(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	code := r.URL.Query().Get("code")
	if state == "" || code == "" {
		writeError(w, 400, "invalid_oauth_callback", "인증 응답이 올바르지 않습니다.")
		return
	}
	var providerID uuid.UUID
	var encrypted []byte
	var redirectURI string
	err := s.DB.QueryRow(r.Context(), `DELETE FROM oauth_states WHERE state_hash=$1 AND expires_at>now() RETURNING provider_id,verifier_encrypted,redirect_uri`, cryptox.Digest(state)).Scan(&providerID, &encrypted, &redirectURI)
	if err != nil {
		writeError(w, 400, "oauth_state_expired", "인증 요청이 만료되었거나 이미 사용되었습니다.")
		return
	}
	provider, err := s.loadAuthProviderByID(r.Context(), providerID)
	if err != nil || !provider.Enabled {
		writeError(w, 404, "provider_not_found", "인증 제공자를 찾을 수 없습니다.")
		return
	}
	verifier, err := s.Box.Decrypt(encrypted, "oauth:"+state)
	if err != nil {
		writeError(w, 400, "oauth_state_invalid", "인증 요청을 확인하지 못했습니다.")
		return
	}
	config, oidcProvider, err := s.oauthConfig(r.Context(), r, provider, redirectURI)
	if err != nil {
		writeError(w, 502, "provider_unavailable", "인증 제공자에 연결하지 못했습니다.")
		return
	}
	token, err := config.Exchange(r.Context(), code, oauth2.SetAuthURLParam("code_verifier", string(verifier)))
	if err != nil {
		s.Logger.Warn("oauth exchange failed", "provider", provider.Slug, "error", err)
		writeError(w, 401, "oauth_exchange_failed", "인증 코드를 확인하지 못했습니다.")
		return
	}
	claims, err := fetchClaims(r.Context(), provider, oidcProvider, config, token)
	if err != nil {
		s.Logger.Warn("oauth claims failed", "provider", provider.Slug, "error", err)
		writeError(w, 401, "oauth_profile_failed", "인증 사용자 정보를 가져오지 못했습니다.")
		return
	}
	subject := claimString(claims, mappingValue(provider.ClaimMapping, "subject", "sub"))
	email := claimString(claims, mappingValue(provider.ClaimMapping, "email", "email"))
	name := claimString(claims, mappingValue(provider.ClaimMapping, "name", "name"))
	if subject == "" {
		writeError(w, 401, "oauth_subject_missing", "인증 제공자의 사용자 식별자가 없습니다.")
		return
	}
	userID, err := s.upsertExternalUser(r.Context(), provider, subject, email, name, claims)
	if err != nil {
		s.Logger.Error("external user upsert failed", "error", err)
		writeError(w, 500, "oauth_user_failed", "사용자 계정을 연결하지 못했습니다.")
		return
	}
	if err := s.issueSession(w, r, userID); err != nil {
		writeError(w, 500, "session_failed", "세션을 만들지 못했습니다.")
		return
	}
	auditRequest := r.WithContext(context.WithValue(r.Context(), principalKey, Principal{UserID: userID, Roles: []string{}}))
	s.audit(auditRequest, "auth.oauth_login", "user", userID.String(), nil, map[string]any{"provider": provider.Slug}, "success")
	http.Redirect(w, r, "/", http.StatusFound)
}

func (s *Server) loadAuthProvider(ctx context.Context, slug string, enabledOnly bool) (authProvider, error) {
	query := `SELECT id,slug,provider_type,preset,name,enabled,issuer_url,authorization_url,token_url,userinfo_url,client_id,client_secret_encrypted,scopes,claim_mapping,options FROM auth_providers WHERE slug=$1`
	if enabledOnly {
		query += ` AND enabled`
	}
	return s.scanAuthProvider(s.DB.QueryRow(ctx, query, slug))
}

func (s *Server) loadAuthProviderByID(ctx context.Context, id uuid.UUID) (authProvider, error) {
	return s.scanAuthProvider(s.DB.QueryRow(ctx, `SELECT id,slug,provider_type,preset,name,enabled,issuer_url,authorization_url,token_url,userinfo_url,client_id,client_secret_encrypted,scopes,claim_mapping,options FROM auth_providers WHERE id=$1`, id))
}

type rowScanner interface{ Scan(...any) error }

func (s *Server) scanAuthProvider(row rowScanner) (authProvider, error) {
	var p authProvider
	var mapping, options []byte
	err := row.Scan(&p.ID, &p.Slug, &p.ProviderType, &p.Preset, &p.Name, &p.Enabled, &p.IssuerURL, &p.AuthorizationURL, &p.TokenURL, &p.UserinfoURL, &p.ClientID, &p.ClientSecretEncrypted, &p.Scopes, &mapping, &options)
	if err != nil {
		return p, err
	}
	_ = json.Unmarshal(mapping, &p.ClaimMapping)
	_ = json.Unmarshal(options, &p.Options)
	return p, nil
}

func (s *Server) oauthConfig(ctx context.Context, r *http.Request, p authProvider, savedRedirect string) (*oauth2.Config, *oidc.Provider, error) {
	secret := ""
	if len(p.ClientSecretEncrypted) > 0 {
		plain, err := s.Box.Decrypt(p.ClientSecretEncrypted, "auth-provider:"+p.ID.String())
		if err != nil {
			return nil, nil, err
		}
		secret = string(plain)
	}
	redirect := savedRedirect
	if redirect == "" {
		redirect = s.oauthRedirectURL(r, p.Slug)
	}
	endpoint := oauth2.Endpoint{}
	var discovered *oidc.Provider
	if p.IssuerURL != nil && *p.IssuerURL != "" {
		var err error
		discovered, err = oidc.NewProvider(ctx, *p.IssuerURL)
		if err != nil {
			return nil, nil, err
		}
		endpoint = discovered.Endpoint()
	} else {
		if p.AuthorizationURL == nil || p.TokenURL == nil {
			return nil, nil, fmt.Errorf("authorization_url and token_url are required")
		}
		endpoint = oauth2.Endpoint{AuthURL: *p.AuthorizationURL, TokenURL: *p.TokenURL}
	}
	return &oauth2.Config{ClientID: p.ClientID, ClientSecret: secret, Endpoint: endpoint, RedirectURL: redirect, Scopes: p.Scopes}, discovered, nil
}

func (s *Server) oauthRedirectURL(r *http.Request, slug string) string {
	if settings, err := s.settingObject(r, "auth.oauth"); err == nil {
		if base, ok := settings["callback_base_url"].(string); ok && strings.TrimSpace(base) != "" {
			return strings.TrimRight(base, "/") + "/api/v1/auth/oauth/" + url.PathEscape(slug) + "/callback"
		}
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host + "/api/v1/auth/oauth/" + url.PathEscape(slug) + "/callback"
}

func fetchClaims(ctx context.Context, p authProvider, discovered *oidc.Provider, config *oauth2.Config, token *oauth2.Token) (map[string]any, error) {
	claims := make(map[string]any)
	if discovered != nil {
		raw, ok := token.Extra("id_token").(string)
		if !ok {
			return nil, fmt.Errorf("id_token missing")
		}
		idToken, err := discovered.Verifier(&oidc.Config{ClientID: config.ClientID}).Verify(ctx, raw)
		if err != nil {
			return nil, err
		}
		if err := idToken.Claims(&claims); err != nil {
			return nil, err
		}
		return claims, nil
	}
	if p.UserinfoURL == nil {
		return nil, fmt.Errorf("userinfo_url missing")
	}
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, *p.UserinfoURL, nil)
	response, err := config.Client(ctx, token).Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 2048))
		return nil, fmt.Errorf("userinfo status %d: %s", response.StatusCode, body)
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&claims); err != nil {
		return nil, err
	}
	return claims, nil
}

func mappingValue(mapping map[string]any, key, fallback string) string {
	if value, ok := mapping[key].(string); ok && value != "" {
		return value
	}
	return fallback
}

func claimString(claims map[string]any, path string) string {
	var current any = claims
	for _, part := range strings.Split(path, ".") {
		object, ok := current.(map[string]any)
		if !ok {
			return ""
		}
		current, ok = object[part]
		if !ok {
			return ""
		}
	}
	switch value := current.(type) {
	case string:
		return value
	case float64:
		return fmt.Sprintf("%.0f", value)
	default:
		return fmt.Sprint(value)
	}
}

var usernameClean = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

func (s *Server) upsertExternalUser(ctx context.Context, p authProvider, subject, email, name string, claims map[string]any) (uuid.UUID, error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var userID uuid.UUID
	err = tx.QueryRow(ctx, `SELECT user_id FROM external_identities WHERE provider_id=$1 AND subject=$2`, p.ID, subject).Scan(&userID)
	if err == pgx.ErrNoRows && email != "" {
		err = tx.QueryRow(ctx, `SELECT id FROM users WHERE lower(email)=lower($1) AND status='active'`, email).Scan(&userID)
	}
	if err != nil && err != pgx.ErrNoRows {
		return uuid.Nil, err
	}
	if err == pgx.ErrNoRows {
		userID = uuid.New()
		base := strings.Split(email, "@")[0]
		if base == "" {
			base = p.Slug + "-" + subject
		}
		base = strings.Trim(usernameClean.ReplaceAllString(base, "-"), "-")
		if base == "" {
			base = "user"
		}
		username := base
		for suffix := 0; ; suffix++ {
			if suffix > 0 {
				username = fmt.Sprintf("%s-%d", base, suffix)
			}
			var exists bool
			_ = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE lower(username)=lower($1))`, username).Scan(&exists)
			if !exists {
				break
			}
		}
		if name == "" {
			name = username
		}
		if _, err := tx.Exec(ctx, `INSERT INTO users(id,username,email,display_name,last_login_at) VALUES($1,$2,$3,$4,now())`, userID, username, nullableString(email), name); err != nil {
			return uuid.Nil, err
		}
		roles := []string{"buyer"}
		// The setting controls safe default roles; unknown roles are ignored by FK.
		var settingRaw []byte
		if tx.QueryRow(ctx, `SELECT value FROM system_settings WHERE key='auth.oauth'`).Scan(&settingRaw) == nil {
			var setting struct {
				DefaultRoles []string `json:"default_roles"`
			}
			if json.Unmarshal(settingRaw, &setting) == nil && len(setting.DefaultRoles) > 0 {
				roles = setting.DefaultRoles
			}
		}
		for _, role := range roles {
			_, _ = tx.Exec(ctx, `INSERT INTO user_roles(user_id,role_code) VALUES($1,$2) ON CONFLICT DO NOTHING`, userID, role)
		}
	}
	claimsJSON, _ := json.Marshal(claims)
	_, err = tx.Exec(ctx, `INSERT INTO external_identities(id,user_id,provider_id,subject,claims) VALUES($1,$2,$3,$4,$5)
		ON CONFLICT(provider_id,subject) DO UPDATE SET claims=EXCLUDED.claims,last_login_at=now()`, uuid.New(), userID, p.ID, subject, claimsJSON)
	if err != nil {
		return uuid.Nil, err
	}
	_, _ = tx.Exec(ctx, `UPDATE users SET last_login_at=now() WHERE id=$1`, userID)
	return userID, tx.Commit(ctx)
}
