package httpapi

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/hkjang/Kkiit/internal/analytics"
)

// trackingServer serves the embedded shell with the given tracking settings
// already cached, which is how the page path is exercised without a database.
func trackingServer(config analytics.Config) *Server {
	server := &Server{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	server.analyticsCache, server.analyticsLoaded = &config, time.Now()
	return server
}

func get(t *testing.T, handler http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
	return recorder
}

var scriptTag = regexp.MustCompile(`(?i)<script[^>]*>`)

func TestTrackingOffLeavesPagesAndPolicyUntouched(t *testing.T) {
	handler := trackingServer(analytics.Config{}).Handler()
	for _, path := range []string{"/", "/talents/1", "/admin/settings"} {
		response := get(t, handler, path)
		if response.Code != http.StatusOK {
			t.Fatalf("%s: status=%d", path, response.Code)
		}
		if strings.Contains(response.Body.String(), "nonce=") || strings.Contains(response.Body.String(), "tracker.js") {
			t.Fatalf("%s: 꺼진 상태인데 스니펫이 있습니다", path)
		}
		policy := response.Header().Get("Content-Security-Policy")
		if policy != basePolicy {
			t.Fatalf("%s: 꺼진 상태의 정책이 원래와 다릅니다: %s", path, policy)
		}
		if strings.Contains(policy, "report-uri") || strings.Contains(policy, "nonce-") {
			t.Fatalf("%s: 꺼진 상태에 추적 정책이 남아 있습니다: %s", path, policy)
		}
	}
	if strings.Contains(basePolicy, "'unsafe-inline'") && !strings.Contains(basePolicy, "style-src 'self' 'unsafe-inline'") {
		t.Fatal("스크립트 정책에 'unsafe-inline' 이 들어갔습니다")
	}
	if !strings.Contains(basePolicy, "script-src 'self';") {
		t.Fatalf("script-src 가 'self' 로 잠겨 있지 않습니다: %s", basePolicy)
	}
}

func TestTrackingOnInjectsNoncedSnippetAndWidensPolicyOnlyForIt(t *testing.T) {
	config := analytics.Config{Enabled: true, Provider: analytics.ProviderCustom, Placement: "head", CustomSnippet: `<script src="https://tracker.corp.example/t.js"></script>
<script>window.__t={endpoint:"https://tracker.corp.example/collect"}</script>`}
	handler := trackingServer(config).Handler()
	response := get(t, handler, "/talents/1")
	body := response.Body.String()
	policy := response.Header().Get("Content-Security-Policy")

	nonces := regexp.MustCompile(`'nonce-([A-Za-z0-9_-]+)'`).FindStringSubmatch(policy)
	if len(nonces) != 2 || len(nonces[1]) < 16 {
		t.Fatalf("정책에 nonce 가 없습니다: %s", policy)
	}
	nonce := nonces[1]
	tags := scriptTag.FindAllString(body, -1)
	var snippetTags int
	for _, tag := range tags {
		if strings.Contains(tag, "/assets/") {
			continue // the application bundle is 'self' and needs no nonce
		}
		snippetTags++
		if !strings.Contains(tag, `nonce="`+nonce+`"`) {
			t.Fatalf("스니펫 script 태그에 요청의 nonce 가 없습니다: %s", tag)
		}
	}
	if snippetTags != 2 {
		t.Fatalf("스니펫 태그가 %d개입니다: %s", snippetTags, body)
	}
	head := strings.Index(strings.ToLower(body), "</head>")
	if head < 0 || !strings.Contains(body[:head], "tracker.corp.example") {
		t.Fatal("placement=head 인데 스니펫이 head 안에 없습니다")
	}
	for _, want := range []string{"script-src 'self' 'nonce-" + nonce + "' https://tracker.corp.example", "connect-src 'self' https://tracker.corp.example", "img-src 'self' data: blob: https://tracker.corp.example", "report-uri " + cspReportPath, "style-src 'self' 'unsafe-inline'", "object-src 'none'"} {
		if !strings.Contains(policy, want) {
			t.Fatalf("정책에 %q 가 없습니다: %s", want, policy)
		}
	}
	if strings.Contains(strings.Split(policy, "style-src")[0], "'unsafe-inline'") {
		t.Fatalf("script-src 에 'unsafe-inline' 이 들어갔습니다: %s", policy)
	}
	if second := get(t, handler, "/"); strings.Contains(second.Header().Get("Content-Security-Policy"), nonce) {
		t.Fatal("두 요청이 같은 nonce 를 받았습니다")
	}

	config.Placement = "body"
	body = get(t, trackingServer(config).Handler(), "/").Body.String()
	head = strings.Index(strings.ToLower(body), "</head>")
	tail := strings.LastIndex(strings.ToLower(body), "</body>")
	if !strings.Contains(body[head:tail], "tracker.corp.example") {
		t.Fatal("placement=body 인데 스니펫이 body 끝에 없습니다")
	}
}

func TestTrackingSkipsAdminPagesUnlessAsked(t *testing.T) {
	config := analytics.Config{Enabled: true, Provider: analytics.ProviderMomento, MomentoURL: "https://momento.corp.example", MomentoSiteID: "kkiit", MomentoProxy: true, Placement: "head"}
	handler := trackingServer(config).Handler()
	for _, path := range []string{"/admin", "/admin/settings"} {
		response := get(t, handler, path)
		if strings.Contains(response.Body.String(), "tracker.js") || strings.Contains(response.Header().Get("Content-Security-Policy"), "nonce-") {
			t.Fatalf("%s: include_admin 이 꺼져 있는데 관리 화면에 붙었습니다", path)
		}
	}
	page := get(t, handler, "/")
	if !strings.Contains(page.Body.String(), `src="/momento/tracker.js"`) || !strings.Contains(page.Body.String(), `data-endpoint="/momento"`) {
		t.Fatalf("Momento 프록시 스니펫이 없습니다: %s", page.Body.String())
	}
	policy := page.Header().Get("Content-Security-Policy")
	if strings.Contains(policy, "momento.corp.example") || !strings.Contains(policy, "connect-src 'self';") {
		t.Fatalf("프록시 모드인데 정책에 외부 출처가 있습니다: %s", policy)
	}
	config.IncludeAdmin = true
	if !strings.Contains(get(t, trackingServer(config).Handler(), "/admin/settings").Body.String(), "tracker.js") {
		t.Fatal("include_admin 이 켜졌는데 관리 화면에 붙지 않았습니다")
	}
}

func TestNonPagePathsCarryNoSnippetAndANarrowPolicy(t *testing.T) {
	config := analytics.Config{Enabled: true, Provider: analytics.ProviderCustom, Placement: "head", CustomSnippet: `<script src="https://tracker.corp.example/t.js"></script>`}
	handler := trackingServer(config).Handler()
	for _, path := range []string{"/api/v1/version", "/health/live", "/momento/tracker.js"} {
		response := get(t, handler, path)
		if got := response.Header().Get("Content-Security-Policy"); got != nonPagePolicy {
			t.Fatalf("%s: 비화면 경로의 정책이 좁지 않습니다: %s", path, got)
		}
		if strings.Contains(response.Body.String(), "tracker.corp.example") {
			t.Fatalf("%s: 비화면 경로에 스니펫이 있습니다", path)
		}
	}
	// The proxy is only open for Momento with the proxy switched on; for any
	// other provider the path is simply not there.
	if response := get(t, handler, "/momento/tracker.js"); response.Code != http.StatusNotFound {
		t.Fatalf("프록시가 닫혀 있어야 하는데 status=%d", response.Code)
	}
}

func TestMomentoProxyForwardsWithoutCookies(t *testing.T) {
	var seen *http.Request
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Clone(r.Context())
		w.Header().Set("Content-Type", "application/javascript")
		_, _ = w.Write([]byte("// tracker"))
	}))
	defer collector.Close()
	config := analytics.Config{Enabled: true, Provider: analytics.ProviderMomento, MomentoURL: collector.URL + "/base/", MomentoSiteID: "kkiit", MomentoProxy: true, Placement: "head"}
	handler := trackingServer(config).Handler()
	request := httptest.NewRequest(http.MethodGet, "/momento/tracker.js?v=1", nil)
	request.Header.Set("Cookie", "kkiit_session=secret")
	request.Header.Set("Authorization", "Bearer key")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || recorder.Body.String() != "// tracker" {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if seen == nil || seen.URL.Path != "/base/tracker.js" || seen.URL.RawQuery != "v=1" {
		t.Fatalf("수집기가 받은 경로가 잘못되었습니다: %+v", seen.URL)
	}
	if seen.Header.Get("Cookie") != "" || seen.Header.Get("Authorization") != "" {
		t.Fatal("세션 쿠키나 인증 헤더가 수집기로 전달되었습니다")
	}
	if recorder.Header().Get("Content-Security-Policy") != nonPagePolicy {
		t.Fatalf("프록시 응답의 정책이 좁지 않습니다: %s", recorder.Header().Get("Content-Security-Policy"))
	}
}

func TestCSPReportIsRecordedOnceAndAlwaysAnswered(t *testing.T) {
	server := &Server{}
	body := `{"csp-report":{"blocked-uri":"https://momento.corp.example/collect/v1/events","effective-directive":"connect-src","document-uri":"https://kkiit.example/talents/1"}}`
	for range 3 {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, cspReportPath, strings.NewReader(body))
		request.Header.Set("Content-Type", "application/csp-report")
		server.receiveCSPReport(recorder, request)
		if recorder.Code != http.StatusNoContent {
			t.Fatalf("status=%d", recorder.Code)
		}
	}
	items := server.violations.List(analytics.Config{})
	if len(items) != 1 || items[0].Origin != "https://momento.corp.example" || items[0].Directive != "connect-src" || items[0].Count != 3 {
		t.Fatalf("기록이 잘못되었습니다: %#v", items)
	}
	broken := httptest.NewRecorder()
	server.receiveCSPReport(broken, httptest.NewRequest(http.MethodPost, cspReportPath, strings.NewReader("{not json")))
	if broken.Code != http.StatusNoContent || len(server.violations.List(analytics.Config{})) != 1 {
		t.Fatalf("깨진 신고: status=%d items=%d", broken.Code, len(server.violations.List(analytics.Config{})))
	}
	oversized := httptest.NewRecorder()
	server.receiveCSPReport(oversized, httptest.NewRequest(http.MethodPost, cspReportPath, strings.NewReader(strings.Repeat("x", maxReportBytes*2))))
	if oversized.Code != http.StatusNoContent {
		t.Fatalf("큰 본문: status=%d", oversized.Code)
	}

	listed := httptest.NewRecorder()
	server.listAnalyticsViolations(listed, httptest.NewRequest(http.MethodGet, "/api/v1/admin/analytics/violations", nil))
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), `"origin":"https://momento.corp.example"`) {
		t.Fatalf("목록: status=%d body=%s", listed.Code, listed.Body.String())
	}
	cleared := httptest.NewRecorder()
	server.clearAnalyticsViolations(cleared, httptest.NewRequest(http.MethodDelete, "/api/v1/admin/analytics/violations", nil))
	if cleared.Code != http.StatusNoContent || len(server.violations.List(analytics.Config{})) != 0 {
		t.Fatal("기록이 비워지지 않았습니다")
	}
}

func TestPutSettingRejectsOversizedOrUnusableTracking(t *testing.T) {
	server := &Server{}
	put := func(payload string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPut, "/api/v1/admin/settings/analytics.tracking", strings.NewReader(payload))
		request.SetPathValue("key", analytics.SettingKey)
		server.putSetting(recorder, request)
		return recorder
	}
	huge := put(`{"value":{"enabled":false,"provider":"custom","custom_snippet":"` + strings.Repeat("a", analytics.MaxSnippetBytes+1) + `"}}`)
	if huge.Code != http.StatusBadRequest || !strings.Contains(huge.Body.String(), "invalid_tracking") {
		t.Fatalf("8KB 를 넘는 스니펫이 거부되지 않았습니다: status=%d body=%s", huge.Code, huge.Body.String())
	}
	if response := put(`{"value":{"enabled":true,"provider":"momento","momento_url":"","momento_site_id":""}}`); response.Code != http.StatusBadRequest {
		t.Fatalf("주소 없이 켠 Momento 가 거부되지 않았습니다: status=%d", response.Code)
	}
	if response := put(`{"value":"on"}`); response.Code != http.StatusBadRequest {
		t.Fatalf("객체가 아닌 값이 거부되지 않았습니다: status=%d", response.Code)
	}
	// A valid value passes validation and only then reaches the database,
	// which this test does not have; the recovered panic proves the point.
	defer func() { _ = recover() }()
	valid := put(`{"value":{"enabled":false,"provider":"momento"}}`)
	if valid.Code == http.StatusBadRequest {
		t.Fatalf("올바른 값이 거부되었습니다: %s", valid.Body.String())
	}
}

func TestAllowAnalyticsHostRejectsNonHTTPOrigins(t *testing.T) {
	server := &Server{}
	for _, origin := range []string{"", "momento.corp.example", "chrome-extension://abc", "data:text/plain,x"} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/analytics/violations/allow", strings.NewReader(`{"origin":"`+origin+`"}`))
		server.allowAnalyticsHost(recorder, request)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("%q: status=%d", origin, recorder.Code)
		}
	}
}
