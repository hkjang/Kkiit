package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// mcpRaw posts one JSON-RPC request with whatever bearer it is given and
// returns the response as is, for the cases where the status and the headers
// are the thing under test.
func mcpRaw(t *testing.T, server *httptest.Server, bearer, method string, params any) (*http.Response, map[string]any) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	request, _ := http.NewRequest(http.MethodPost, server.URL+"/mcp", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	if bearer != "" {
		request.Header.Set("Authorization", "Bearer "+bearer)
	}
	response, err := (&http.Client{Timeout: 20 * time.Second}).Do(request)
	if err != nil {
		t.Fatalf("POST /mcp: %v", err)
	}
	defer response.Body.Close()
	payload, _ := io.ReadAll(response.Body)
	var decoded map[string]any
	_ = json.Unmarshal(payload, &decoded)
	return response, decoded
}

func errorMessage(decoded map[string]any) string {
	object, _ := decoded["error"].(map[string]any)
	return fmt.Sprint(object["message"])
}

func toolText(decoded map[string]any) string {
	result, _ := decoded["result"].(map[string]any)
	content, _ := result["content"].([]any)
	if len(content) == 0 {
		return ""
	}
	first, _ := content[0].(map[string]any)
	return fmt.Sprint(first["text"])
}

func TestIntegrationMCPAcceptsSsoTokensForRegisteredAccountsOnly(t *testing.T) {
	server, pool := integrationServer(t)
	issuer := newFakeIssuer(t)
	admin := operatorClient(t, server, pool, "mcpssoadmin")
	grantRole(t, pool, operatorUsername(t, pool, admin), "super_admin")
	resource := "https://market.example.test/mcp"
	metadataURL := "https://market.example.test/.well-known/oauth-protected-resource/mcp"

	slug := "mcpsso-" + uniqueName("p")
	created := admin.do(http.MethodPost, "/api/v1/admin/auth-providers", map[string]any{
		"slug": slug, "name": "사내 Keycloak", "preset": "keycloak", "provider_type": "oidc", "enabled": true,
		"issuer_url": issuer.server.URL, "client_id": "kkiit", "client_secret": "secret",
		"scopes": []string{"openid", "email"}, "claim_mapping": map[string]any{}, "options": map[string]any{},
	}, http.StatusCreated)
	providerID := fmt.Sprint(created["id"])
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM auth_providers WHERE id=$1::uuid`, providerID)
	})
	// The database outlives a run, so the row starts and ends as a fresh
	// installation writes it.
	resetSetting := func() {
		_, _ = pool.Exec(context.Background(), `UPDATE system_settings SET value='{"enabled":false,"provider":"","resource":"","audience":[],"scopes":["mcp.use"]}'::jsonb WHERE key='mcp.oauth'`)
	}
	resetSetting()
	t.Cleanup(resetSetting)

	// Off by default: no metadata, no challenge, and a token is refused the
	// way any unknown bearer always was.
	visitor := newClient(t, server.URL)
	if response, err := visitor.raw(http.MethodGet, "/.well-known/oauth-protected-resource"); err != nil || response.StatusCode != http.StatusNotFound {
		t.Fatalf("metadata while off: %v %v", response.StatusCode, err)
	}
	response, decoded := mcpRaw(t, server, issuer.accessToken(t, "nobody", "claude-mcp", map[string]any{"aud": resource}), "tools/list", map[string]any{})
	if response.StatusCode != http.StatusUnauthorized || response.Header.Get("WWW-Authenticate") != "" || errorMessage(decoded) != "로그인이 필요합니다." {
		t.Fatalf("token while off: status=%d header=%q body=%v", response.StatusCode, response.Header.Get("WWW-Authenticate"), decoded)
	}

	// Turning it on is refused until it could work: a resource identifier
	// (or the external address) and a provider that exists.
	settings := func() (map[string]any, float64) {
		list := admin.do(http.MethodGet, "/api/v1/admin/settings", nil, http.StatusOK)
		for _, item := range list["items"].([]any) {
			entry := item.(map[string]any)
			if entry["key"] == "mcp.oauth" {
				return entry["value"].(map[string]any), entry["version"].(float64)
			}
		}
		t.Fatal("mcp.oauth setting row missing")
		return nil, 0
	}
	value, version := settings()
	if value["enabled"] != false {
		t.Fatalf("fresh row is not off: %v", value)
	}
	put := func(changes map[string]any, want int) map[string]any {
		value, version := settings()
		for key, item := range changes {
			value[key] = item
		}
		return admin.do(http.MethodPut, "/api/v1/admin/settings/mcp.oauth", map[string]any{"value": value, "version": version}, want)
	}
	_ = version
	refused := put(map[string]any{"enabled": true, "provider": slug}, http.StatusBadRequest)
	if !strings.Contains(errorMessage(refused), "resource") {
		t.Fatalf("enabling without an address was not refused for it: %v", refused)
	}
	refused = put(map[string]any{"enabled": true, "provider": "no-such-provider", "resource": resource}, http.StatusBadRequest)
	if !strings.Contains(errorMessage(refused), "no-such-provider") {
		t.Fatalf("unknown provider was not named: %v", refused)
	}
	put(map[string]any{"enabled": true, "provider": slug, "resource": resource, "audience": []string{}, "scopes": []string{"mcp.use"}}, http.StatusOK)

	// The metadata is bare JSON on both paths, readable from a browser.
	for _, path := range []string{"/.well-known/oauth-protected-resource", "/.well-known/oauth-protected-resource/mcp"} {
		response, err := visitor.raw(http.MethodGet, path)
		if err != nil || response.StatusCode != http.StatusOK {
			t.Fatalf("metadata %s: status=%d err=%v", path, response.StatusCode, err)
		}
		var document map[string]any
		_ = json.NewDecoder(response.Body).Decode(&document)
		response.Body.Close()
		if document["resource"] != resource || fmt.Sprint(document["authorization_servers"]) != "["+issuer.server.URL+"]" || response.Header.Get("Access-Control-Allow-Origin") != "*" {
			t.Fatalf("metadata %s: %v cors=%q", path, document, response.Header.Get("Access-Control-Allow-Origin"))
		}
		if _, enveloped := document["error"]; enveloped || document["bearer_methods_supported"] == nil {
			t.Fatalf("metadata %s is not the bare RFC 9728 document: %v", path, document)
		}
	}

	// The 401 on /mcp now points at the metadata; the REST 401 does not.
	response, _ = mcpRaw(t, server, "", "tools/list", map[string]any{})
	if response.StatusCode != http.StatusUnauthorized || response.Header.Get("WWW-Authenticate") != `Bearer realm="Kkiit", resource_metadata="`+metadataURL+`"` {
		t.Fatalf("challenge: status=%d header=%q", response.StatusCode, response.Header.Get("WWW-Authenticate"))
	}
	restRequest, _ := http.NewRequest(http.MethodGet, server.URL+"/api/v1/me", nil)
	restResponse, err := http.DefaultClient.Do(restRequest)
	if err != nil {
		t.Fatal(err)
	}
	restResponse.Body.Close()
	if restResponse.StatusCode != http.StatusUnauthorized || restResponse.Header.Get("WWW-Authenticate") != "" {
		t.Fatalf("rest 401 carries the challenge: status=%d header=%q", restResponse.StatusCode, restResponse.Header.Get("WWW-Authenticate"))
	}

	// A token for somebody who never signed in to the web is refused, and
	// nobody is created for it.
	response, decoded = mcpRaw(t, server, issuer.accessToken(t, "stranger", "claude-mcp", map[string]any{"aud": resource}), "tools/list", map[string]any{})
	if response.StatusCode != http.StatusUnauthorized || !strings.Contains(errorMessage(decoded), "먼저 웹에서") || !strings.HasSuffix(response.Header.Get("WWW-Authenticate"), `error="invalid_token"`) {
		t.Fatalf("unregistered subject: status=%d header=%q body=%v", response.StatusCode, response.Header.Get("WWW-Authenticate"), decoded)
	}
	var strangers int
	_ = pool.QueryRow(context.Background(), `SELECT count(*) FROM external_identities WHERE provider_id=$1::uuid AND subject='stranger'`, providerID).Scan(&strangers)
	if strangers != 0 {
		t.Fatal("a token created an identity")
	}

	// Signing in to the web once is what registers an account. The fake
	// issuer makes the code the subject of the ID token it mints.
	subject := uniqueName("ssouser")
	browser := browserClient(t, server.URL)
	location := browser.redirect("/api/v1/auth/oauth/" + slug + "/start")
	browser.redirect("/api/v1/auth/oauth/" + slug + "/callback?state=" + url.QueryEscape(location.Query().Get("state")) + "&code=" + subject)
	browser.do(http.MethodGet, "/api/v1/me", nil, http.StatusOK)

	// Minted for another application in the realm: refused, and the message
	// says what was seen and what to configure.
	response, decoded = mcpRaw(t, server, issuer.accessToken(t, subject, "other-app", nil), "tools/list", map[string]any{})
	if response.StatusCode != http.StatusUnauthorized || !strings.Contains(errorMessage(decoded), `azp "other-app"`) || !strings.Contains(errorMessage(decoded), resource) {
		t.Fatalf("foreign token: status=%d body=%v", response.StatusCode, decoded)
	}
	// Minted for us through an Audience mapper: the catalogue opens.
	mapped := issuer.accessToken(t, subject, "claude-mcp", map[string]any{"aud": []string{"account", resource}})
	response, decoded = mcpRaw(t, server, mapped, "tools/list", map[string]any{})
	if response.StatusCode != http.StatusOK || decoded["result"] == nil {
		t.Fatalf("mapped token: status=%d body=%v", response.StatusCode, decoded)
	}
	// Without a mapper, the administrator lists the client id instead.
	plain := issuer.accessToken(t, subject, "claude-mcp", nil)
	if response, _ = mcpRaw(t, server, plain, "tools/list", map[string]any{}); response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("azp accepted before it was listed: status=%d", response.StatusCode)
	}
	put(map[string]any{"audience": []string{"claude-mcp"}}, http.StatusOK)
	if response, _ = mcpRaw(t, server, plain, "tools/list", map[string]any{}); response.StatusCode != http.StatusOK {
		t.Fatalf("azp refused after it was listed: status=%d", response.StatusCode)
	}

	// The token opens what the administrator's scopes allow, narrowed by the
	// account's own roles; nothing in the token widens it.
	_, decoded = mcpRaw(t, server, plain, "tools/call", map[string]any{"name": "list_orders", "arguments": map[string]any{}})
	if text := toolText(decoded); strings.Contains(text, "필요한 키 권한") {
		t.Fatalf("mcp.use tool refused: %s", text)
	}
	_, decoded = mcpRaw(t, server, plain, "tools/call", map[string]any{"name": "preview_coupon", "arguments": map[string]any{"code": "X", "talent_id": "x"}})
	if text := toolText(decoded); !strings.Contains(text, "필요한 키 권한이 없습니다: orders.buy") {
		t.Fatalf("orders.buy tool ran on mcp.use alone: %s", text)
	}
	put(map[string]any{"scopes": []string{"mcp.use", "orders.buy", "orders.sell"}}, http.StatusOK)
	_, decoded = mcpRaw(t, server, plain, "tools/call", map[string]any{"name": "preview_coupon", "arguments": map[string]any{"code": "X", "talent_id": "x"}})
	if text := toolText(decoded); strings.Contains(text, "필요한 키 권한") {
		t.Fatalf("orders.buy tool refused after the scope was granted: %s", text)
	}
	// orders.sell is in the list but not in a buyer's roles.
	_, decoded = mcpRaw(t, server, plain, "tools/call", map[string]any{"name": "list_settlements", "arguments": map[string]any{}})
	if text := toolText(decoded); !strings.Contains(text, "필요한 키 권한이 없습니다: orders.sell") {
		t.Fatalf("a scope outside the account's roles was granted: %s", text)
	}

	// The token is a credential for /mcp only.
	restRequest, _ = http.NewRequest(http.MethodGet, server.URL+"/api/v1/me", nil)
	restRequest.Header.Set("Authorization", "Bearer "+plain)
	if restResponse, err = http.DefaultClient.Do(restRequest); err != nil {
		t.Fatal(err)
	}
	restResponse.Body.Close()
	if restResponse.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a valid token opened REST: status=%d", restResponse.StatusCode)
	}

	// A suspended account does not come back to life over MCP.
	var userID string
	if err := pool.QueryRow(context.Background(), `SELECT user_id::text FROM external_identities WHERE provider_id=$1::uuid AND subject=$2`, providerID, subject).Scan(&userID); err != nil {
		t.Fatalf("identity row: %v", err)
	}
	admin.do(http.MethodPatch, "/api/v1/admin/users/"+userID, map[string]any{"status": "suspended", "display_name": subject}, http.StatusOK)
	response, decoded = mcpRaw(t, server, plain, "tools/list", map[string]any{})
	if response.StatusCode != http.StatusUnauthorized || !strings.Contains(errorMessage(decoded), "비활성") {
		t.Fatalf("suspended account: status=%d body=%v", response.StatusCode, decoded)
	}
	admin.do(http.MethodPatch, "/api/v1/admin/users/"+userID, map[string]any{"status": "active", "display_name": subject}, http.StatusOK)

	// Switched off again everything is as it was, with the token still valid.
	put(map[string]any{"enabled": false}, http.StatusOK)
	response, decoded = mcpRaw(t, server, plain, "tools/list", map[string]any{})
	if response.StatusCode != http.StatusUnauthorized || response.Header.Get("WWW-Authenticate") != "" || errorMessage(decoded) != "로그인이 필요합니다." {
		t.Fatalf("token after switching off: status=%d header=%q body=%v", response.StatusCode, response.Header.Get("WWW-Authenticate"), decoded)
	}
	if response, err := visitor.raw(http.MethodGet, "/.well-known/oauth-protected-resource/mcp"); err != nil || response.StatusCode != http.StatusNotFound {
		t.Fatalf("metadata after switching off: %v %v", response.StatusCode, err)
	}
}
