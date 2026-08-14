package httpapi

import (
	"net/http/httptest"
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

func TestValidateApprovalPolicyRejectsInvalidConditions(t *testing.T) {
	valid := approvalPolicyInput{ResourceType: "talent_publish", Name: "고액 주문", Conditions: map[string]any{"min_amount": float64(100_000)}, Steps: []map[string]any{{"role": "operator", "min_approvals": float64(1)}}}
	if !validateApprovalPolicy(&valid) {
		t.Fatal("expected valid approval policy")
	}
	invalid := approvalPolicyInput{ResourceType: "talent_publish", Name: "잘못된 범위", Conditions: map[string]any{"min_amount": float64(200), "max_amount": float64(100)}, Steps: []map[string]any{{"role": "operator", "min_approvals": float64(1)}}}
	if validateApprovalPolicy(&invalid) {
		t.Fatal("minimum greater than maximum must be rejected")
	}
}
