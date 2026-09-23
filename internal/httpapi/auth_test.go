package httpapi

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestExternalRequestBaseURLUsesProxyHeaders(t *testing.T) {
	request := httptest.NewRequest("GET", "http://kkiit:8080/api/v1/auth/oauth/keycloak/start", nil)
	request.Header.Set("X-Forwarded-Proto", "https")
	request.Header.Set("X-Forwarded-Host", "market.example.com")

	if got, want := externalRequestBaseURL(request), "https://market.example.com"; got != want {
		t.Fatalf("externalRequestBaseURL()=%q, want %q", got, want)
	}
}

func TestExternalRequestBaseURLUsesStandardForwardedHeader(t *testing.T) {
	request := httptest.NewRequest("GET", "http://kkiit:8080/", nil)
	request.Header.Set("Forwarded", `for=192.0.2.1;proto=https;host="market.example.com:8443"`)

	if got, want := externalRequestBaseURL(request), "https://market.example.com:8443"; got != want {
		t.Fatalf("externalRequestBaseURL()=%q, want %q", got, want)
	}
}

func TestNormalizeExternalBaseURL(t *testing.T) {
	if got, ok := normalizeExternalBaseURL("https://market.example.com/kkiit/"); !ok || got != "https://market.example.com/kkiit" {
		t.Fatalf("unexpected normalized URL %q (ok=%v)", got, ok)
	}
	for _, value := range []string{"javascript:alert(1)", "https://user@example.com", "https://example.com?next=bad"} {
		if _, ok := normalizeExternalBaseURL(value); ok {
			t.Errorf("unsafe external base URL accepted: %q", value)
		}
	}
}

func TestOAuthCallbackURLEscapesProviderSlug(t *testing.T) {
	if got, want := oauthCallbackURL("https://market.example.com", "keycloak/internal"), "https://market.example.com/api/v1/auth/oauth/keycloak%2Finternal/callback"; got != want {
		t.Fatalf("oauthCallbackURL()=%q, want %q", got, want)
	}
}

func TestValidateProviderRejectsInvalidSlugAndURL(t *testing.T) {
	valid := providerInput{Slug: "internal-keycloak", Name: "Keycloak", ProviderType: "oidc", ClientID: "kkiit", IssuerURL: "https://sso.example.com/realms/kkiit/"}
	if !validateProvider(&valid) {
		t.Fatal("expected valid Keycloak provider")
	}
	if valid.IssuerURL != "https://sso.example.com/realms/kkiit" {
		t.Fatalf("issuer URL was not normalized: %q", valid.IssuerURL)
	}
	for _, invalid := range []providerInput{
		{Slug: "bad/slug", Name: "Keycloak", ProviderType: "oidc", ClientID: "kkiit", IssuerURL: "https://sso.example.com/realms/kkiit"},
		{Slug: "keycloak", Name: "Keycloak", ProviderType: "oidc", ClientID: "kkiit", IssuerURL: "http://"},
	} {
		if validateProvider(&invalid) {
			t.Errorf("invalid provider accepted: %+v", invalid)
		}
	}
}

func TestSafeReturnToAcceptsOnlySameOriginPaths(t *testing.T) {
	for _, value := range []string{"/", "/orders/42", "/talents/7?tab=reviews", "/profile/security#mfa"} {
		if !safeReturnTo(value) {
			t.Errorf("same-origin path refused: %q", value)
		}
	}
	for _, value := range []string{"", "orders", "//evil.example.com/", "/\\evil.example.com", "https://evil.example.com/", "javascript:alert(1)", "/orders\r\nSet-Cookie: x=y"} {
		if safeReturnTo(value) {
			t.Errorf("unsafe return_to accepted: %q", value)
		}
	}
}

func TestProviderAutoLoginIsOffUnlessAnOIDCProviderSaysOtherwise(t *testing.T) {
	if providerAutoLogin(authProvider{ProviderType: "oidc"}) {
		t.Fatal("auto_login must default to off")
	}
	if providerAutoLogin(authProvider{ProviderType: "oidc", Options: map[string]any{"auto_login": "true"}}) {
		t.Fatal("a non-boolean option must not turn auto_login on")
	}
	if providerAutoLogin(authProvider{ProviderType: "oauth2", Options: map[string]any{"auto_login": true}}) {
		t.Fatal("prompt=none is an OIDC parameter; an OAuth2 provider cannot use it")
	}
	if !providerAutoLogin(authProvider{ProviderType: "oidc", Options: map[string]any{"auto_login": true}}) {
		t.Fatal("expected auto_login on")
	}
}

func TestValidateApprovalPolicyRejectsInvalidConditions(t *testing.T) {
	valid := approvalPolicyInput{ResourceType: "talent_publish", Name: "고액 주문", Conditions: map[string]any{"min_amount": float64(100_000)}, Steps: []map[string]any{{"role": "operator", "min_approvals": float64(1)}}}
	if _, ok := validateApprovalPolicy(&valid); !ok {
		t.Fatal("expected valid approval policy")
	}
	invalid := approvalPolicyInput{ResourceType: "talent_publish", Name: "잘못된 범위", Conditions: map[string]any{"min_amount": float64(200), "max_amount": float64(100)}, Steps: []map[string]any{{"role": "operator", "min_approvals": float64(1)}}}
	if _, ok := validateApprovalPolicy(&invalid); ok {
		t.Fatal("minimum greater than maximum must be rejected")
	}
}

// The matcher skips service_types and seller_levels whenever the stored value
// is not a []any of strings, so a policy saved with a bare string applies to
// every product instead of the few it names. Saving has to refuse it.
func TestValidateApprovalPolicyRejectsInvalidArrayConditions(t *testing.T) {
	for _, key := range []string{"service_types", "seller_levels"} {
		for _, broken := range []struct {
			name  string
			value any
		}{
			{"배열이 아닌 문자열", "design"},
			{"원소가 숫자", []any{"design", float64(3)}},
			{"공백뿐인 문자열", []any{"design", "   "}},
		} {
			policy := approvalPolicyInput{ResourceType: "talent_publish", Name: "배열 조건", Conditions: map[string]any{key: broken.value}, Steps: []map[string]any{{"role": "operator", "min_approvals": float64(1)}}}
			reason, ok := validateApprovalPolicy(&policy)
			if ok {
				t.Fatalf("%s의 %s는 거부되어야 합니다", key, broken.name)
			}
			// The administrator has to learn which key to correct.
			if !strings.Contains(reason, key) {
				t.Fatalf("%s의 %s 거부 사유에 키 이름이 없습니다: %q", key, broken.name, reason)
			}
		}
		// An empty list and an absent key keep working: the matcher only applies
		// the condition when it holds a value, so refusing them here would change
		// which products existing policies cover.
		for _, allowed := range []struct {
			name       string
			conditions map[string]any
		}{
			{"정상 배열", map[string]any{key: []any{"design"}}},
			{"빈 배열", map[string]any{key: []any{}}},
			{"키 없음", map[string]any{}},
		} {
			policy := approvalPolicyInput{ResourceType: "talent_publish", Name: "배열 조건", Conditions: allowed.conditions, Steps: []map[string]any{{"role": "operator", "min_approvals": float64(1)}}}
			if reason, ok := validateApprovalPolicy(&policy); !ok {
				t.Fatalf("%s의 %s는 허용되어야 합니다: %s", key, allowed.name, reason)
			}
		}
	}
}
