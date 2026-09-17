package httpapi

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestMCPOAuthConfigReadsArraysAndSpaceSeparatedStrings(t *testing.T) {
	fresh := readMCPOAuthConfig(map[string]any{"enabled": false, "provider": "", "resource": "", "audience": []any{}, "scopes": []any{"mcp.use"}})
	if fresh.Enabled || len(fresh.Audience) != 0 || strings.Join(fresh.Scopes, " ") != "mcp.use" {
		t.Fatalf("fresh row: %+v", fresh)
	}
	byHand := readMCPOAuthConfig(map[string]any{"enabled": true, "provider": " keycloak ", "resource": "https://market.example.com/mcp/", "audience": "claude-mcp, cursor-mcp", "scopes": "mcp.use orders.buy"})
	if byHand.Provider != "keycloak" || byHand.Resource != "https://market.example.com/mcp" {
		t.Fatalf("trimming: %+v", byHand)
	}
	if strings.Join(byHand.Audience, " ") != "claude-mcp cursor-mcp" || strings.Join(byHand.Scopes, " ") != "mcp.use orders.buy" {
		t.Fatalf("lists: %+v", byHand)
	}
	if missing := readMCPOAuthConfig(map[string]any{}); strings.Join(missing.Scopes, " ") != "mcp.use" {
		t.Fatalf("scopes default: %+v", missing)
	}
}

func TestMCPOAuthConfigValidateNamesTheField(t *testing.T) {
	for _, tc := range []struct {
		name   string
		values map[string]any
		want   string
	}{
		{"relative resource", map[string]any{"resource": "/mcp"}, "절대 주소"},
		{"resource with query", map[string]any{"resource": "https://a.example/mcp?x=1"}, "절대 주소"},
		{"resource off the mcp path", map[string]any{"resource": "https://a.example/api"}, "MCP 경로"},
		{"enabled without scopes", map[string]any{"enabled": true, "scopes": []any{}}, "scopes"},
	} {
		err := readMCPOAuthConfig(tc.values).validate()
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err=%v want %q", tc.name, err, tc.want)
		}
	}
	if err := readMCPOAuthConfig(map[string]any{"enabled": true, "resource": "https://a.example/mcp"}).validate(); err != nil {
		t.Fatalf("valid row refused: %v", err)
	}
	// Off with an empty scope list is allowed: nothing reads it yet.
	if err := readMCPOAuthConfig(map[string]any{"enabled": false, "scopes": []any{}}).validate(); err != nil {
		t.Fatalf("switched off row refused: %v", err)
	}
}

func TestLooksLikeJWT(t *testing.T) {
	for token, want := range map[string]bool{"a.b.c": true, "kkiit_abc_def": false, "a.b": false, "a..c": false, "": false, "a.b.c.d": false} {
		if got := looksLikeJWT(token); got != want {
			t.Errorf("%q: got %v want %v", token, got, want)
		}
	}
}

func TestMCPOAuthResourceComesFromSettingsBeforeTheHostHeader(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8080/mcp", nil)
	request.Header.Set("X-Forwarded-Proto", "https")
	request.Header.Set("X-Forwarded-Host", "market.example.com")
	explicit := mcpOAuthState{mcpOAuthConfig: mcpOAuthConfig{Resource: "https://mcp.example.com/mcp"}, callbackBase: "https://market.example.com"}
	if got := explicit.resource(request); got != "https://mcp.example.com/mcp" {
		t.Fatalf("explicit resource: %s", got)
	}
	if got := explicit.metadataURL(request); got != "https://mcp.example.com/.well-known/oauth-protected-resource/mcp" {
		t.Fatalf("metadata url: %s", got)
	}
	external := mcpOAuthState{callbackBase: "https://market.example.com/"}
	if got := external.resource(request); got != "https://market.example.com/mcp" {
		t.Fatalf("external address resource: %s", got)
	}
	if got := (mcpOAuthState{}).resource(request); got != "https://market.example.com/mcp" {
		t.Fatalf("host fallback: %s", got)
	}
}

// Without a database nothing is configured: the metadata is 404, the MCP 401
// carries no challenge and a JWT on /mcp is simply not a credential.
func TestMCPOAuthIsInertWhenNothingIsConfigured(t *testing.T) {
	server := &Server{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	recorder := httptest.NewRecorder()
	server.protectedResourceMetadata(recorder, httptest.NewRequest(http.MethodGet, "/.well-known/oauth-protected-resource", nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("metadata while off: status=%d", recorder.Code)
	}
	recorder = httptest.NewRecorder()
	server.mcpChallenge(func(http.ResponseWriter, *http.Request) { t.Fatal("handler reached without a principal") })(recorder, httptest.NewRequest(http.MethodPost, "/mcp", nil))
	if recorder.Code != http.StatusUnauthorized || recorder.Header().Get("WWW-Authenticate") != "" {
		t.Fatalf("challenge while off: status=%d header=%q", recorder.Code, recorder.Header().Get("WWW-Authenticate"))
	}
	if !strings.Contains(recorder.Body.String(), "authentication_required") {
		t.Fatalf("body changed while off: %s", recorder.Body.String())
	}
	var seen bool
	handler := server.authentication(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = true
		if _, ok := principalFrom(r.Context()); ok {
			t.Fatal("a jwt produced a principal with nothing configured")
		}
		if refusal, _ := r.Context().Value(mcpOAuthRefusalKey).(string); refusal != "" {
			t.Fatalf("a refusal was recorded while off: %s", refusal)
		}
	}))
	request := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	request.Header.Set("Authorization", "Bearer eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJ4In0.c2ln")
	handler.ServeHTTP(httptest.NewRecorder(), request)
	if !seen {
		t.Fatal("request did not reach the handler")
	}
}

// The token checks that need no account: signature, issuer, expiry, kind,
// binding and audience, each refused for its own reason and the audience
// refusal naming what was seen and what to configure.
func TestMCPOAuthTokenChecks(t *testing.T) {
	issuer := newFakeIssuer(t)
	server := &Server{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	issuerURL := issuer.server.URL
	state := mcpOAuthState{
		mcpOAuthConfig: mcpOAuthConfig{Enabled: true, Resource: "https://market.example.com/mcp", Audience: []string{"claude-mcp"}},
		provider:       authProvider{ID: uuid.New(), Name: "Keycloak", IssuerURL: &issuerURL, ClaimMapping: map[string]any{}},
		active:         true,
	}
	request := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	verify := func(token string) (string, error) {
		return server.verifyMCPOAuthToken(context.Background(), request, state, token)
	}

	// Minted for us: aud names the resource (Audience mapper), or azp is a
	// client the administrator listed (no mapper).
	if subject, err := verify(issuer.accessToken(t, "alice", "other-app", map[string]any{"aud": []string{"account", "https://market.example.com/mcp"}})); err != nil || subject != "alice" {
		t.Fatalf("aud=resource: subject=%q err=%v", subject, err)
	}
	if subject, err := verify(issuer.accessToken(t, "alice", "claude-mcp", nil)); err != nil || subject != "alice" {
		t.Fatalf("azp listed: subject=%q err=%v", subject, err)
	}
	if _, err := verify(issuer.accessToken(t, "alice", "other-app", map[string]any{"aud": "claude-mcp"})); err != nil {
		t.Fatalf("aud listed: %v", err)
	}

	// Minted for another application in the realm.
	_, err := verify(issuer.accessToken(t, "alice", "other-app", nil))
	if err == nil {
		t.Fatal("a token for another client was accepted")
	}
	for _, want := range []string{"[account]", `azp "other-app"`, `mcp.oauth.audience 에 "other-app"`, `"https://market.example.com/mcp"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("audience refusal lacks %q: %s", want, err)
		}
	}

	refusals := map[string]struct {
		token string
		want  string
	}{
		"expired":      {issuer.accessToken(t, "alice", "claude-mcp", map[string]any{"exp": 1_000_000}), "만료"},
		"not yet":      {issuer.accessToken(t, "alice", "claude-mcp", map[string]any{"nbf": 4_000_000_000}), "유효하지 않습니다"},
		"other issuer": {issuer.accessToken(t, "alice", "claude-mcp", map[string]any{"iss": "https://elsewhere.example/realms/x"}), "유효하지 않습니다"},
		"id token":     {issuer.accessToken(t, "alice", "claude-mcp", map[string]any{"typ": "ID"}), "ID 토큰"},
		"bound":        {issuer.accessToken(t, "alice", "claude-mcp", map[string]any{"cnf": map[string]any{"jkt": "x"}}), "cnf"},
		"no subject":   {issuer.accessToken(t, "alice", "claude-mcp", map[string]any{"sub": nil}), "sub"},
		"hs256":        {hmacToken(t, issuerURL, "claude-mcp"), "유효하지 않습니다"},
		"unsigned":     {jwtEncode(t, map[string]any{"alg": "none"}) + "." + jwtEncode(t, map[string]any{"iss": issuerURL, "sub": "alice", "azp": "claude-mcp", "exp": 4_000_000_000}) + ".x", "유효하지 않습니다"},
	}
	for name, tc := range refusals {
		_, err := verify(tc.token)
		if err == nil {
			t.Errorf("%s: accepted", name)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err=%v want %q", name, err, tc.want)
		}
	}
	if server.mcpOAuthProviders[issuerURL] == nil {
		t.Fatal("discovery was not cached")
	}
}

// hmacToken is what an attacker who read the public JWKS could forge if HS*
// were accepted: the public key bytes used as the HMAC secret.
func hmacToken(t *testing.T, issuer, clientID string) string {
	t.Helper()
	signingInput := jwtEncode(t, map[string]any{"alg": "HS256", "kid": "test", "typ": "JWT"}) + "." + jwtEncode(t, map[string]any{"iss": issuer, "sub": "alice", "azp": clientID, "typ": "Bearer", "exp": 4_000_000_000})
	mac := hmac.New(sha256.New, []byte("public-key-bytes"))
	mac.Write([]byte(signingInput))
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
