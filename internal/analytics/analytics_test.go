package analytics

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func momento(proxy bool) Config {
	return Config{Enabled: true, Provider: ProviderMomento, MomentoURL: "https://momento.corp.example/", MomentoSiteID: "kkiit", MomentoProxy: proxy, Placement: "head"}
}

func TestDefaultIsOffAndReadConfigKeepsDefaults(t *testing.T) {
	if Default().Enabled || !Default().MomentoProxy || Default().Provider != ProviderMomento {
		t.Fatalf("기본값이 바뀌었습니다: %+v", Default())
	}
	config := ReadConfig(map[string]any{"enabled": "yes", "provider": "GA4", "placement": "sidebar", "momento_proxy": nil})
	if config.Enabled || config.Provider != ProviderGA4 || config.Placement != "head" || !config.MomentoProxy {
		t.Fatalf("잘못된 형식의 값이 기본값으로 돌아가지 않았습니다: %+v", config)
	}
	if ReadConfig(nil).Active("/") {
		t.Fatal("빈 설정에서 추적이 켜졌습니다")
	}
}

func TestActiveRespectsAdminPagesAndProvider(t *testing.T) {
	config := momento(true)
	if !config.Active("/") || !config.Active("/talents/1") {
		t.Fatal("켜진 설정이 일반 화면에서 비활성입니다")
	}
	if config.Active("/admin") || config.Active("/admin/settings") {
		t.Fatal("include_admin 이 꺼져 있는데 관리 화면에 붙습니다")
	}
	if config.Active("/administrators") == false {
		t.Fatal("/admin 접두사 검사가 다른 경로까지 막습니다")
	}
	config.IncludeAdmin = true
	if !config.Active("/admin/settings") {
		t.Fatal("include_admin 이 켜졌는데 관리 화면에 붙지 않습니다")
	}
	config.Enabled = false
	if config.Active("/") {
		t.Fatal("꺼진 설정이 활성입니다")
	}
	none := Config{Enabled: true, Provider: ProviderNone}
	if none.Active("/") {
		t.Fatal("provider none 이 활성입니다")
	}
}

func TestMomentoSnippetUsesProxyByDefault(t *testing.T) {
	snippet := momento(true).Snippet("n0nce")
	for _, want := range []string{`src="/momento/tracker.js"`, `data-endpoint="/momento"`, `data-site-id="kkiit"`, `data-contract-version="1"`, `nonce="n0nce"`} {
		if !strings.Contains(snippet, want) {
			t.Fatalf("스니펫에 %s 가 없습니다: %s", want, snippet)
		}
	}
	if strings.Contains(snippet, "momento.corp.example") {
		t.Fatalf("프록시 모드인데 외부 주소가 스니펫에 있습니다: %s", snippet)
	}
	scripts, connects, images := momento(true).PolicySources()
	if len(scripts)+len(connects)+len(images) != 0 {
		t.Fatalf("프록시 모드에서는 정책에 외부 출처가 없어야 합니다: %v %v %v", scripts, connects, images)
	}

	direct := momento(false).Snippet("n0nce")
	if !strings.Contains(direct, `src="https://momento.corp.example/tracker.js"`) || strings.Contains(direct, "data-endpoint") {
		t.Fatalf("직접 모드 스니펫이 잘못되었습니다: %s", direct)
	}
	scripts, connects, images = momento(false).PolicySources()
	if len(scripts) != 1 || scripts[0] != "https://momento.corp.example" || len(connects) != 1 || len(images) != 1 {
		t.Fatalf("직접 모드 출처가 잘못되었습니다: %v %v %v", scripts, connects, images)
	}
	if !momento(true).ProxyActive() || momento(false).ProxyActive() {
		t.Fatal("ProxyActive 가 momento_proxy 를 따르지 않습니다")
	}
}

func TestSnippetEscapesAdministratorInput(t *testing.T) {
	config := momento(false)
	config.MomentoSiteID = `x"><script>alert(1)</script>`
	snippet := config.Snippet("n")
	if strings.Contains(snippet, "<script>alert") {
		t.Fatalf("사이트 id 가 이스케이프되지 않았습니다: %s", snippet)
	}
}

func TestNonceIsAddedToEveryScriptTag(t *testing.T) {
	config := Config{Enabled: true, Provider: ProviderCustom, CustomSnippet: `<SCRIPT src="https://t.example/a.js"></SCRIPT>
<script nonce="keep">x()</script>
<script>y()</script>`}
	snippet := config.Snippet("abc")
	if strings.Count(snippet, `nonce="abc"`) != 2 || !strings.Contains(snippet, `nonce="keep"`) {
		t.Fatalf("nonce 가 모든 script 태그에 붙지 않았습니다: %s", snippet)
	}
	if strings.Count(strings.ToLower(snippet), "<script") != 3 {
		t.Fatalf("태그 수가 달라졌습니다: %s", snippet)
	}
}

func TestSnippetOriginsAreReadFromCustomSnippet(t *testing.T) {
	snippet := `<script src="https://tracker.corp.example/t.js"></script>
<script>window.__t={endpoint:"https://tracker.corp.example/collect",pixel:'http://pixel.corp.example:8080/p.gif?id=1'};fetch('data:text/plain,x')</script>`
	origins := SnippetOrigins(snippet)
	if len(origins) != 2 || origins[0] != "https://tracker.corp.example" || origins[1] != "http://pixel.corp.example:8080" {
		t.Fatalf("출처 추출이 잘못되었습니다: %v", origins)
	}
	config := Config{Enabled: true, Provider: ProviderCustom, CustomSnippet: snippet, AllowedHosts: "https://extra.example,\nhttps://other.example/"}
	scripts, _, _ := config.PolicySources()
	joined := strings.Join(scripts, " ")
	for _, want := range []string{"https://tracker.corp.example", "http://pixel.corp.example:8080", "https://extra.example", "https://other.example"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("%s 가 script-src 출처에 없습니다: %s", want, joined)
		}
	}
}

func TestValidateRejectsWhatCannotWork(t *testing.T) {
	big := Config{Provider: ProviderCustom, CustomSnippet: strings.Repeat("x", MaxSnippetBytes+1)}
	if big.Validate() == nil {
		t.Fatal("8KB 를 넘는 스니펫이 통과했습니다")
	}
	if (Config{Provider: "pixel", Placement: "head"}).Validate() == nil {
		t.Fatal("알 수 없는 provider 가 통과했습니다")
	}
	if (Config{Provider: ProviderMomento, Placement: "head", MomentoURL: "momento.corp.example"}).Validate() == nil {
		t.Fatal("스킴 없는 수집기 주소가 통과했습니다")
	}
	off := Config{Provider: ProviderMomento, Placement: "head"}
	if err := off.Validate(); err != nil {
		t.Fatalf("꺼진 설정은 빈 채로 저장할 수 있어야 합니다: %v", err)
	}
	on := off
	on.Enabled = true
	if on.Validate() == nil {
		t.Fatal("주소 없이 켠 Momento 가 통과했습니다")
	}
	if err := momento(true).Validate(); err != nil {
		t.Fatalf("올바른 설정이 거부되었습니다: %v", err)
	}
	if (Config{Enabled: true, Provider: ProviderCustom, Placement: "body", CustomSnippet: " "}).Validate() == nil {
		t.Fatal("빈 custom 스니펫이 통과했습니다")
	}
}

func TestRecorderKeepsDistinctOriginsAndMarksAllowed(t *testing.T) {
	now := time.Date(2026, 9, 12, 11, 0, 0, 0, time.UTC)
	store := &Recorder{now: func() time.Time { now = now.Add(time.Second); return now }}
	for range 5 {
		store.Record("https://momento.corp.example/collect/v1/events", "connect-src", "https://kkiit.example/")
	}
	store.Record("chrome-extension://abc/inject.js", "script-src", "/")
	store.Record("data:text/plain,x", "img-src", "/")
	items := store.List(Config{})
	if len(items) != 1 || items[0].Origin != "https://momento.corp.example" || items[0].Count != 5 || items[0].Directive != "connect-src" || items[0].Allowed {
		t.Fatalf("기록이 잘못되었습니다: %#v", items)
	}
	allowed := store.List(Config{Provider: ProviderCustom, AllowedHosts: "https://momento.corp.example"})
	if !allowed[0].Allowed {
		t.Fatal("허용 목록에 넣은 출처가 여전히 차단으로 표시됩니다")
	}
	wildcard := &Recorder{}
	wildcard.Record("https://region1.google-analytics.com/g/collect", "connect-src 'self'", "/")
	if listed := wildcard.List(Config{Provider: ProviderGA4, MeasurementID: "G-1"}); len(listed) != 1 || !listed[0].Allowed {
		t.Fatalf("와일드카드 정책 항목이 인식되지 않았습니다: %#v", listed)
	}
	store.Forget()
	if len(store.List(Config{})) != 0 {
		t.Fatal("Forget 뒤에도 기록이 남아 있습니다")
	}
}

func TestRecorderIsBounded(t *testing.T) {
	store := &Recorder{}
	for index := range MaxViolations + 20 {
		store.Record(fmt.Sprintf("https://host%d.example/p", index), "script-src", "/")
	}
	if got := len(store.List(Config{})); got > MaxViolations {
		t.Fatalf("기록이 %d개까지 늘었습니다", got)
	}
}

func TestAddAllowedHost(t *testing.T) {
	if got := AddAllowedHost("", "https://a.example/"); got != "https://a.example" {
		t.Fatalf("첫 항목: %q", got)
	}
	if got := AddAllowedHost("https://a.example", "https://b.example"); got != "https://a.example, https://b.example" {
		t.Fatalf("두 번째 항목: %q", got)
	}
	if got := AddAllowedHost("https://a.example, https://b.example", "HTTPS://A.EXAMPLE"); got != "https://a.example, https://b.example" {
		t.Fatalf("중복이 추가되었습니다: %q", got)
	}
}
