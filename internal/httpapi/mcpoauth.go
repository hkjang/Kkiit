package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/jackc/pgx/v5"
)

// MCP over SSO — /mcp opened with a Keycloak access token instead of a key.
//
// The MCP authorization specification (2025-06-18 and later) is OAuth 2.1:
// the MCP server is a resource server that publishes where its authorization
// server is (RFC 9728), and a client refused with 401 reads that document,
// sends the person through the authorization server with PKCE and comes back
// with an access token whose audience (RFC 8707) is this server. Nothing about
// issuing tokens happens here — the OIDC provider the web sign-in already
// uses does that. This file answers two questions only: where is the
// authorization server, and was this token issued for us.
//
// The personal key stays. A token is a second door into the same room: it
// authenticates an account that already exists, carries the key permissions
// the administrator chose, and never creates an account. Signing in to the
// web once is what registers somebody; a program presenting a token is not
// the moment to decide who they are.

const (
	mcpOAuthSettingKey   = "mcp.oauth"
	mcpOAuthMetadataPath = "/.well-known/oauth-protected-resource"
	mcpPath              = "/mcp"
	// mcpOAuthIdPRequestsPerMinute bounds discovery and JWKS fetches. go-oidc
	// refetches the key set on every unknown key id, so without a bound a
	// stream of forged tokens with random kids would turn into a stream of
	// requests to Keycloak.
	mcpOAuthIdPRequestsPerMinute = 30
	mcpOAuthIdPTimeout           = 10 * time.Second
	mcpOAuthIdPBodyLimit         = 1 << 20
)

// mcpOAuthSigningAlgs is the allow list: asymmetric only. HS* would let anybody
// who can read the JWKS forge a token, and "none" is not a signature.
var mcpOAuthSigningAlgs = []string{oidc.RS256, oidc.RS384, oidc.RS512, oidc.ES256, oidc.ES384, oidc.ES512, oidc.PS256, oidc.PS384, oidc.PS512}

// mcpOAuthConfig is the mcp.oauth setting row.
type mcpOAuthConfig struct {
	Enabled bool
	// Provider is the slug of the auth_providers row whose issuer is the
	// authorization server. Empty means the only enabled OIDC provider.
	Provider string
	// Resource is the identifier this server claims (RFC 8707): the public
	// address of /mcp. Empty means the OAuth external address plus /mcp.
	Resource string
	// Audience lists client ids an administrator accepts in aud or azp, for
	// deployments without an Audience mapper.
	Audience []string
	// Scopes are the key permissions an SSO caller gets, before the
	// account's own roles narrow them. The token's scope claim is not read:
	// Keycloak does not know this application's permission vocabulary.
	Scopes []string
}

func readMCPOAuthConfig(values map[string]any) mcpOAuthConfig {
	config := mcpOAuthConfig{Scopes: []string{"mcp.use"}}
	config.Enabled, _ = values["enabled"].(bool)
	config.Provider = strings.TrimSpace(settingString(values, "provider"))
	config.Resource = strings.TrimRight(strings.TrimSpace(settingString(values, "resource")), "/")
	config.Audience = settingList(values, "audience")
	if _, present := values["scopes"]; present {
		config.Scopes = settingList(values, "scopes")
	}
	return config
}

func settingString(values map[string]any, key string) string {
	value, _ := values[key].(string)
	return value
}

// settingList accepts the array the console writes and the space separated
// string the standard's table describes, so a row edited by hand still reads.
func settingList(values map[string]any, key string) []string {
	var items []string
	switch value := values[key].(type) {
	case []any:
		for _, item := range value {
			if text, ok := item.(string); ok {
				items = append(items, strings.Fields(text)...)
			}
		}
	case string:
		items = strings.Fields(strings.ReplaceAll(value, ",", " "))
	}
	return items
}

// validate refuses what could never work, with the field named. The
// provider and the external address are checked by the caller, which has
// the database.
func (config mcpOAuthConfig) validate() error {
	if config.Resource != "" {
		parsed, err := url.Parse(config.Resource)
		if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.User != nil {
			return errors.New("resource 는 https://host/mcp 꼴의 절대 주소여야 합니다(쿼리·프래그먼트 없이)")
		}
		if !strings.HasSuffix(parsed.Path, mcpPath) {
			return errors.New("resource 는 MCP 경로(" + mcpPath + ")로 끝나야 합니다")
		}
	}
	if config.Enabled && len(config.Scopes) == 0 {
		return errors.New("scopes 가 비어 있으면 SSO 로 들어온 사람이 아무것도 할 수 없습니다. mcp.use 를 넣으세요")
	}
	return nil
}

// mcpOAuthState is the setting resolved against the rest of the deployment:
// the provider row that is the authorization server, and the two other
// settings the resource identifier and the rate limit come from.
type mcpOAuthState struct {
	mcpOAuthConfig
	provider     authProvider
	callbackBase string
	rateLimit    int
	// active is false when the switch is off or when something it needs is
	// missing; reason says which, for the log.
	active bool
	reason string
}

func (s *Server) mcpOAuth(ctx context.Context) mcpOAuthState {
	state := mcpOAuthState{rateLimit: 60}
	if s.DB == nil {
		return state
	}
	rows, err := s.DB.Query(ctx, `SELECT key,value FROM system_settings WHERE key IN ($1,'auth.oauth','api.policy')`, mcpOAuthSettingKey)
	if err != nil {
		return state
	}
	defer rows.Close()
	var found bool
	for rows.Next() {
		var key string
		var raw []byte
		if rows.Scan(&key, &raw) != nil {
			continue
		}
		var values map[string]any
		if json.Unmarshal(raw, &values) != nil {
			continue
		}
		switch key {
		case mcpOAuthSettingKey:
			state.mcpOAuthConfig, found = readMCPOAuthConfig(values), true
		case "auth.oauth":
			if base, ok := values["callback_base_url"].(string); ok {
				if normalized, ok := normalizeExternalBaseURL(base); ok {
					state.callbackBase = normalized
				}
			}
		case "api.policy":
			state.rateLimit = intSetting(values, "default_rate_limit_per_minute", 60)
		}
	}
	if !found || !state.Enabled {
		return state
	}
	if !s.featureEnabled(ctx, "agent_marketplace") {
		return s.mcpOAuthInactive(state, "agent_marketplace 기능이 꺼져 있습니다")
	}
	provider, err := s.resolveMCPOAuthProvider(ctx, state.Provider)
	if err != nil {
		return s.mcpOAuthInactive(state, err.Error())
	}
	state.provider, state.active = provider, true
	return state
}

// mcpOAuthInactive logs why a switched on setting is not in effect, once per
// reason rather than once per request.
func (s *Server) mcpOAuthInactive(state mcpOAuthState, reason string) mcpOAuthState {
	state.reason = reason
	s.mcpOAuthMu.Lock()
	changed := s.mcpOAuthLastReason != reason
	s.mcpOAuthLastReason = reason
	s.mcpOAuthMu.Unlock()
	if changed {
		s.Logger.Warn("mcp oauth is enabled but not in effect", "reason", reason)
	}
	return state
}

// resolveMCPOAuthProvider finds the OIDC provider that is the authorization
// server. The message is for the administrator saving the setting.
func (s *Server) resolveMCPOAuthProvider(ctx context.Context, slug string) (authProvider, error) {
	if slug != "" {
		provider, err := s.loadAuthProvider(ctx, slug, true)
		if errors.Is(err, pgx.ErrNoRows) {
			return authProvider{}, fmt.Errorf("인증 제공자 %q 가 없거나 중지되어 있습니다", slug)
		}
		if err != nil {
			return authProvider{}, err
		}
		if provider.ProviderType != "oidc" || provider.IssuerURL == nil || strings.TrimSpace(*provider.IssuerURL) == "" {
			return authProvider{}, fmt.Errorf("인증 제공자 %q 는 Issuer URL 이 있는 OIDC 제공자가 아닙니다", slug)
		}
		return provider, nil
	}
	rows, err := s.DB.Query(ctx, `SELECT slug FROM auth_providers WHERE enabled AND provider_type='oidc' AND coalesce(issuer_url,'')<>'' ORDER BY slug`)
	if err != nil {
		return authProvider{}, err
	}
	defer rows.Close()
	var slugs []string
	for rows.Next() {
		var candidate string
		if rows.Scan(&candidate) == nil {
			slugs = append(slugs, candidate)
		}
	}
	switch len(slugs) {
	case 0:
		return authProvider{}, errors.New("Issuer URL 이 있는 활성 OIDC 제공자가 없습니다. 인증 연동에서 Keycloak 제공자를 먼저 켜세요")
	case 1:
		return s.loadAuthProvider(ctx, slugs[0], true)
	default:
		return authProvider{}, fmt.Errorf("활성 OIDC 제공자가 여럿입니다(%s). provider 에 하나를 적으세요", strings.Join(slugs, ", "))
	}
}

func (state mcpOAuthState) issuer() string {
	if state.provider.IssuerURL == nil {
		return ""
	}
	return strings.TrimRight(strings.TrimSpace(*state.provider.IssuerURL), "/")
}

// resource is the identifier this deployment claims for /mcp: what the
// metadata document advertises and what a token's aud may name. The request
// host is the last resort — anybody can set that header — and the setting is
// refused at save time unless one of the other two is present.
func (state mcpOAuthState) resource(r *http.Request) string {
	if state.Resource != "" {
		return state.Resource
	}
	if state.callbackBase != "" {
		return strings.TrimRight(state.callbackBase, "/") + mcpPath
	}
	return strings.TrimRight(externalRequestBaseURL(r), "/") + mcpPath
}

// metadataURL is where a refused client is sent to learn the above.
func (state mcpOAuthState) metadataURL(r *http.Request) string {
	return strings.TrimSuffix(state.resource(r), mcpPath) + mcpOAuthMetadataPath + mcpPath
}

// looksLikeJWT is the cheap shape test that keeps a mistyped key from being
// sent to the provider: three non-empty dot separated parts.
func looksLikeJWT(token string) bool {
	parts := strings.Split(token, ".")
	return len(parts) == 3 && parts[0] != "" && parts[1] != "" && parts[2] != ""
}

// mcpOAuthTransport bounds what discovery and key fetches can cost: a timeout,
// a body limit and a request budget shared by every issuer.
type mcpOAuthTransport struct{ server *Server }

func (t mcpOAuthTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if !t.server.allow("mcp-oauth:idp", mcpOAuthIdPRequestsPerMinute, time.Minute) {
		return nil, errors.New("too many requests to the identity provider")
	}
	response, err := http.DefaultTransport.RoundTrip(request)
	if err != nil {
		return nil, err
	}
	response.Body = struct {
		io.Reader
		io.Closer
	}{io.LimitReader(response.Body, mcpOAuthIdPBodyLimit), response.Body}
	return response, nil
}

// mcpOAuthProvider caches discovery per issuer. Discovery is a round trip to
// Keycloak and the JWKS behind it verifies every token; doing that per
// request would put Keycloak's latency in front of every MCP call. go-oidc
// refetches the key set on an unknown key id, so key rotation needs no cache
// invalidation here.
func (s *Server) mcpOAuthProvider(ctx context.Context, issuer string) (*oidc.Provider, error) {
	s.mcpOAuthMu.Lock()
	defer s.mcpOAuthMu.Unlock()
	if provider := s.mcpOAuthProviders[issuer]; provider != nil {
		return provider, nil
	}
	// Discovery must outlive this request: the provider keeps the context
	// for later key fetches.
	client := &http.Client{Timeout: mcpOAuthIdPTimeout, Transport: mcpOAuthTransport{server: s}}
	provider, err := oidc.NewProvider(oidc.ClientContext(context.WithoutCancel(ctx), client), issuer)
	if err != nil {
		return nil, err
	}
	if s.mcpOAuthProviders == nil {
		s.mcpOAuthProviders = map[string]*oidc.Provider{}
	}
	s.mcpOAuthProviders[issuer] = provider
	return provider, nil
}

// mcpOAuthRefusal is the sentence a refused caller should read. Every refusal
// is an invalid_token to the protocol; the message is what an operator fixes
// the configuration from.
type mcpOAuthRefusal struct{ message string }

func (r mcpOAuthRefusal) Error() string { return r.message }

// verifyMCPOAuthToken checks everything about the token that does not need
// an account: signature, issuer, expiry, kind, binding and audience. It
// returns the subject the account is looked up by.
func (s *Server) verifyMCPOAuthToken(ctx context.Context, r *http.Request, state mcpOAuthState, token string) (string, error) {
	provider, err := s.mcpOAuthProvider(ctx, state.issuer())
	if err != nil {
		s.Logger.Warn("mcp oauth discovery failed", "issuer", state.issuer(), "error", err)
		return "", mcpOAuthRefusal{"Keycloak 발급자 정보를 읽지 못해 SSO 토큰을 확인할 수 없습니다. 잠시 후 다시 시도하거나 관리자에게 알리세요."}
	}
	// Signature, issuer, expiry and nbf. The audience is checked below by
	// hand because more than one value is acceptable and the library
	// compares aud alone, not azp.
	verified, err := provider.Verifier(&oidc.Config{SkipClientIDCheck: true, SupportedSigningAlgs: mcpOAuthSigningAlgs}).Verify(ctx, token)
	if err != nil {
		s.Logger.Warn("mcp oauth token rejected", "issuer", state.issuer(), "error", err)
		if strings.Contains(err.Error(), "expired") {
			return "", mcpOAuthRefusal{"SSO 액세스 토큰이 만료되었습니다. 클라이언트에서 다시 로그인하세요."}
		}
		return "", mcpOAuthRefusal{"SSO 액세스 토큰이 유효하지 않습니다(서명·발급자·유효 기간). 클라이언트에서 다시 로그인하세요."}
	}
	var claims map[string]any
	if err := verified.Claims(&claims); err != nil {
		return "", mcpOAuthRefusal{"SSO 토큰의 내용을 읽을 수 없습니다."}
	}
	// An ID token proves a sign in happened; it is not a credential for an
	// API. Keycloak marks its tokens with typ=Bearer or typ=ID in the payload.
	if strings.EqualFold(claimString(claims, "typ"), "ID") {
		return "", mcpOAuthRefusal{"ID 토큰은 MCP 자격이 아닙니다. 액세스 토큰을 보내세요."}
	}
	// cnf binds the token to a key (DPoP, mTLS) this server cannot verify.
	if _, bound := claims["cnf"]; bound {
		return "", mcpOAuthRefusal{"소지자 증명(cnf)이 묶인 토큰은 이 서버가 검증할 수 없습니다. 일반 Bearer 토큰을 보내세요."}
	}
	subject := claimString(claims, mappingValue(state.provider.ClaimMapping, "subject", "sub"))
	if subject == "" {
		return "", mcpOAuthRefusal{"SSO 토큰에 사용자 식별자(sub)가 없습니다."}
	}
	// Whom the token was minted for. A real Keycloak 26 puts the client in
	// azp and only "account" in aud unless an Audience mapper says otherwise,
	// so "aud names us, or aud/azp names a client the administrator listed"
	// are both the token being for this deployment rather than one passed
	// through from another application in the realm.
	resource := state.resource(r)
	azp := claimString(claims, "azp")
	accepted := func(value string) bool {
		return value != "" && (value == resource || slices.Contains(state.Audience, value))
	}
	if !slices.ContainsFunc(verified.Audience, accepted) && !accepted(azp) {
		return "", mcpOAuthRefusal{fmt.Sprintf("SSO 토큰이 이 서버를 위해 발급된 것이 아닙니다(aud %v, azp %q). 관리자가 mcp.oauth.audience 에 %q 를 더하거나, Keycloak 클라이언트에 Audience 매퍼로 %q 를 넣어야 합니다.", verified.Audience, azp, azp, resource)}
	}
	return subject, nil
}

// authenticateMCPOAuth turns a bearer access token into the principal of an
// account that already signed in with that provider, or says why not. The
// third value is the refusal to show; empty when SSO is simply off, so a
// deployment that never turned it on answers exactly as it always did.
func (s *Server) authenticateMCPOAuth(r *http.Request, token string) (Principal, bool, string) {
	ctx := r.Context()
	state := s.mcpOAuth(ctx)
	if !state.active {
		return Principal{}, false, ""
	}
	subject, err := s.verifyMCPOAuthToken(ctx, r, state, token)
	if err != nil {
		return Principal{}, false, err.Error()
	}
	// The same lookup the web sign in makes, without the provisioning half:
	// the identity row the sign in wrote, and only for an active account.
	var p Principal
	var roles, rolePermissions []string
	err = s.DB.QueryRow(ctx, `
		SELECT u.id,u.username,u.email,u.display_name,
		       COALESCE(array_agg(DISTINCT ur.role_code) FILTER (WHERE ur.role_code IS NOT NULL),ARRAY[]::text[]),
		       COALESCE(array_agg(DISTINCT rp.permission_code) FILTER (WHERE rp.permission_code IS NOT NULL),ARRAY[]::text[])
		FROM external_identities ei JOIN users u ON u.id=ei.user_id
		LEFT JOIN user_roles ur ON ur.user_id=u.id
		LEFT JOIN role_permissions rp ON rp.role_code=ur.role_code
		WHERE ei.provider_id=$1 AND ei.subject=$2 AND u.status='active'
		GROUP BY u.id`, state.provider.ID, subject).Scan(&p.UserID, &p.Username, &p.Email, &p.DisplayName, &roles, &rolePermissions)
	if errors.Is(err, pgx.ErrNoRows) {
		return Principal{}, false, fmt.Sprintf("이 SSO 계정은 Kkiit 에 등록되지 않았거나 비활성입니다. 먼저 웹에서 %s 으로 한 번 로그인하세요.", state.provider.Name)
	}
	if err != nil {
		s.Logger.Error("mcp oauth account lookup failed", "error", err)
		return Principal{}, false, "계정을 확인하지 못했습니다. 잠시 후 다시 시도하세요."
	}
	// The same ceiling a key has: the administrator's list, narrowed to what
	// the account's roles allow. Nothing in the token raises it.
	allowed := make(map[string]bool, len(rolePermissions))
	for _, permission := range rolePermissions {
		allowed[permission] = true
	}
	permissions := make([]string, 0, len(state.Scopes))
	for _, scope := range state.Scopes {
		if allowed[scope] && !slices.Contains(permissions, scope) {
			permissions = append(permissions, scope)
		}
	}
	p.Roles, p.Permissions = roles, permissions
	if !s.allow("oauth:"+p.UserID.String(), state.rateLimit, time.Minute) {
		return Principal{}, false, "SSO 토큰의 분당 요청 한도를 초과했습니다. 잠시 후 다시 시도하세요."
	}
	return p, true, ""
}

// mcpChallenge turns the 401 on /mcp into an invitation: the header names the
// metadata document and the MCP client starts the OAuth flow from there.
// Without it a refusal is a dead end. It is set on the MCP path only — a REST
// 401 carrying it would send browsers and other clients somewhere wrong.
func (s *Server) mcpChallenge(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := principalFrom(r.Context()); ok {
			next(w, r)
			return
		}
		refusal, _ := r.Context().Value(mcpOAuthRefusalKey).(string)
		if state := s.mcpOAuth(r.Context()); state.active {
			challenge := fmt.Sprintf(`Bearer realm="Kkiit", resource_metadata=%q`, state.metadataURL(r))
			if refusal != "" {
				challenge += `, error="invalid_token"`
			}
			w.Header().Set("WWW-Authenticate", challenge)
		}
		if refusal != "" {
			writeError(w, http.StatusUnauthorized, "invalid_token", refusal)
			return
		}
		writeError(w, http.StatusUnauthorized, "authentication_required", "로그인이 필요합니다.")
	}
}

// protectedResourceMetadata is RFC 9728: the document a refused MCP client
// reads to find the authorization server. Public by design — it says where
// to sign in, not who is signed in — and bare JSON rather than the product's
// envelope, because the reader is an OAuth client library.
func (s *Server) protectedResourceMetadata(w http.ResponseWriter, r *http.Request) {
	state := s.mcpOAuth(r.Context())
	if !state.active {
		writeError(w, http.StatusNotFound, "mcp_oauth_disabled", "이 서버의 MCP 는 SSO 토큰을 받지 않습니다. 개인 API 키(kkiit_)를 사용하세요.")
		return
	}
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Cache-Control", "public, max-age=300")
	writeJSON(w, http.StatusOK, map[string]any{
		"resource":                 state.resource(r),
		"authorization_servers":    []string{state.issuer()},
		"bearer_methods_supported": []string{"header"},
		"scopes_supported":         state.Scopes,
		"resource_name":            "Kkiit Marketplace MCP",
	})
}

// validateMCPOAuthSetting is what putSetting refuses: a switch that could not
// take effect is refused with the reason, rather than quietly staying off.
func (s *Server) validateMCPOAuthSetting(ctx context.Context, values map[string]any) error {
	config := readMCPOAuthConfig(values)
	if err := config.validate(); err != nil {
		return err
	}
	if !config.Enabled {
		return nil
	}
	if _, err := s.resolveMCPOAuthProvider(ctx, config.Provider); err != nil {
		return err
	}
	if config.Resource == "" {
		base, _ := s.settingObjectCtx(ctx, "auth.oauth")
		if text, _ := base["callback_base_url"].(string); strings.TrimSpace(text) == "" {
			return errors.New("resource 를 적거나 인증 연동의 외부 서비스 주소를 먼저 저장하세요. 접속 주소에서 만든 리소스 식별자는 토큰의 aud 와 어긋나기 쉽습니다")
		}
	}
	return nil
}
