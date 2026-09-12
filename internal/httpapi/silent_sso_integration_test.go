package httpapi

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// fakeIssuer stands in for Keycloak: discovery, a signing key, and a token
// endpoint that mints an ID token for whatever code it is handed. The
// authorization endpoint is never called, because the tests read the redirect
// the server would have sent the browser to instead of following it.
type fakeIssuer struct {
	server *httptest.Server
	key    *rsa.PrivateKey
	// tokenCalls counts exchanges so a refused silent attempt can be shown to
	// never reach the token endpoint.
	tokenCalls int
}

func newFakeIssuer(t *testing.T) *fakeIssuer {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa key: %v", err)
	}
	issuer := &fakeIssuer{key: key}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		base := issuer.server.URL
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer": base, "authorization_endpoint": base + "/auth", "token_endpoint": base + "/token",
			"jwks_uri": base + "/keys", "id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/keys", func(w http.ResponseWriter, r *http.Request) {
		public := key.PublicKey
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]any{{
			"kty": "RSA", "kid": "test", "use": "sig", "alg": "RS256",
			"n": base64.RawURLEncoding.EncodeToString(public.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(public.E)).Bytes()),
		}}})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		issuer.tokenCalls++
		if err := r.ParseForm(); err != nil || r.PostForm.Get("code") == "" {
			http.Error(w, `{"error":"invalid_request"}`, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "access", "token_type": "Bearer", "expires_in": 300,
			"id_token": issuer.idToken(t, "kkiit", r.PostForm.Get("code")),
		})
	})
	issuer.server = httptest.NewServer(mux)
	t.Cleanup(issuer.server.Close)
	return issuer
}

// idToken signs a minimal RS256 ID token. The code doubles as the subject so
// each test run signs in a distinct account.
func (f *fakeIssuer) idToken(t *testing.T, audience, subject string) string {
	t.Helper()
	encode := func(value any) string {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("encode claims: %v", err)
		}
		return base64.RawURLEncoding.EncodeToString(raw)
	}
	now := time.Now().Unix()
	signingInput := encode(map[string]any{"alg": "RS256", "kid": "test", "typ": "JWT"}) + "." + encode(map[string]any{
		"iss": f.server.URL, "sub": subject, "aud": audience, "exp": now + 300, "iat": now,
		"email": subject + "@example.test", "email_verified": true, "name": "조용한 로그인",
	})
	digest := sha256.Sum256([]byte(signingInput))
	signature, err := rsa.SignPKCS1v15(rand.Reader, f.key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatalf("sign id token: %v", err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature)
}

// browserClient keeps cookies like a browser but stops at redirects, so a test
// can read where the server tried to send the person.
func browserClient(t *testing.T, base string) *client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookie jar: %v", err)
	}
	return &client{t: t, base: base, http: &http.Client{Jar: jar, Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

// redirect performs a GET and returns the Location the server answered with.
func (c *client) redirect(path string) *url.URL {
	c.t.Helper()
	response, err := c.raw(http.MethodGet, path)
	if err != nil {
		c.t.Fatalf("GET %s: %v", path, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusFound {
		c.t.Fatalf("GET %s status=%d want=302", path, response.StatusCode)
	}
	location, err := url.Parse(response.Header.Get("Location"))
	if err != nil {
		c.t.Fatalf("GET %s location: %v", path, err)
	}
	return location
}

func TestIntegrationSilentSsoNeverLoopsAndKeepsTheDeepLink(t *testing.T) {
	server, pool := integrationServer(t)
	issuer := newFakeIssuer(t)
	admin := operatorClient(t, server, pool, "silentadmin")
	grantRole(t, pool, operatorUsername(t, pool, admin), "super_admin")

	slug := "silent-" + uniqueName("p")
	provider := map[string]any{
		"slug": slug, "name": "조용한 Keycloak", "preset": "keycloak", "provider_type": "oidc", "enabled": true,
		"issuer_url": issuer.server.URL, "client_id": "kkiit", "client_secret": "secret",
		"scopes": []string{"openid", "email"}, "claim_mapping": map[string]any{}, "options": map[string]any{},
	}
	created := admin.do(http.MethodPost, "/api/v1/admin/auth-providers", provider, http.StatusCreated)
	providerID := fmt.Sprint(created["id"])
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM auth_providers WHERE id=$1::uuid`, providerID) })
	start := "/api/v1/auth/oauth/" + slug + "/start"
	callback := "/api/v1/auth/oauth/" + slug + "/callback"

	// auto_login is off by default, so asking for prompt=none from the address
	// bar is quietly turned into an ordinary sign in, and the provider's
	// refusal is reported the way it always was rather than as a silent miss.
	visitor := browserClient(t, server.URL)
	location := visitor.redirect(start + "?prompt=none&return_to=%2Forders")
	if location.Query().Get("prompt") != "" {
		t.Fatalf("prompt=none was forwarded while auto_login is off: %s", location)
	}
	state := location.Query().Get("state")
	if state == "" {
		t.Fatalf("no state in %s", location)
	}
	response, err := visitor.raw(http.MethodGet, callback+"?state="+url.QueryEscape(state)+"&error=login_required")
	if err != nil {
		t.Fatalf("callback: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("ordinary refusal status=%d want=401", response.StatusCode)
	}

	// The administrator turns it on for this provider; the login screen learns
	// about it from the public provider list.
	provider["options"] = map[string]any{"auto_login": true}
	admin.do(http.MethodPut, "/api/v1/admin/auth-providers/"+providerID, provider, http.StatusOK)
	published := visitor.do(http.MethodGet, "/api/v1/auth/providers", nil, http.StatusOK)
	var found bool
	for _, item := range published["items"].([]any) {
		entry := item.(map[string]any)
		if entry["slug"] == slug {
			found = entry["auto_login"] == true
		}
	}
	if !found {
		t.Fatalf("provider list does not publish auto_login: %v", published)
	}

	// Without a provider session prompt=none answers login_required. That is an
	// ordinary reply: the browser lands on the login page with the marker that
	// tells it not to try again, and the token endpoint is never contacted.
	location = visitor.redirect(start + "?prompt=none&return_to=%2Forders")
	if location.Query().Get("prompt") != "none" {
		t.Fatalf("prompt=none missing from %s", location)
	}
	state = location.Query().Get("state")
	refused := visitor.redirect(callback + "?state=" + url.QueryEscape(state) + "&error=login_required&error_description=Login+required")
	if refused.String() != silentSsoRefusedPath {
		t.Fatalf("refused silent attempt went to %s, want %s", refused, silentSsoRefusedPath)
	}
	if issuer.tokenCalls != 0 {
		t.Fatalf("a refused silent attempt reached the token endpoint %d times", issuer.tokenCalls)
	}
	// The state was consumed, so replaying the refusal no longer looks silent.
	response, err = visitor.raw(http.MethodGet, callback+"?state="+url.QueryEscape(state)+"&error=login_required")
	if err != nil {
		t.Fatalf("callback replay: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("replayed refusal status=%d want=401", response.StatusCode)
	}
	visitor.do(http.MethodGet, "/api/v1/me", nil, http.StatusUnauthorized)

	// With a provider session the code comes straight back and the person
	// lands where they were going, signed in, without seeing a login screen.
	location = visitor.redirect(start + "?prompt=none&return_to=" + url.QueryEscape("/orders/42?tab=deliveries"))
	state = location.Query().Get("state")
	landed := visitor.redirect(callback + "?state=" + url.QueryEscape(state) + "&code=" + uniqueName("code"))
	if landed.String() != "/orders/42?tab=deliveries" {
		t.Fatalf("silent sign in landed on %s, want the deep link", landed)
	}
	me := visitor.do(http.MethodGet, "/api/v1/me", nil, http.StatusOK)
	if !strings.HasPrefix(fmt.Sprint(me["username"]), "code") {
		t.Fatalf("no session after silent sign in: %v", me)
	}

	// A return_to that would leave the site is dropped, not followed.
	other := browserClient(t, server.URL)
	location = other.redirect(start + "?prompt=none&return_to=" + url.QueryEscape("//evil.example.test/phish"))
	state = location.Query().Get("state")
	landed = other.redirect(callback + "?state=" + url.QueryEscape(state) + "&code=" + uniqueName("code"))
	if landed.String() != "/" {
		t.Fatalf("off-site return_to was followed: %s", landed)
	}
	if !strings.HasPrefix(fmt.Sprint(other.do(http.MethodGet, "/api/v1/me", nil, http.StatusOK)["username"]), "code") {
		t.Fatal("expected the auto-created account to be signed in")
	}
}
