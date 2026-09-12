// Package analytics injects a visitor tracking snippet into the served pages.
//
// The content security policy Kkiit ships with allows scripts from its own
// origin only, so a tracking snippet cannot simply be pasted into the page.
// This package produces both halves of the answer: the markup to inject and
// the policy sources it needs, with a per-request nonce so inline code runs
// without weakening the policy for everything else.
package analytics

import (
	"fmt"
	"html"
	"net/url"
	"strings"
)

const (
	ProviderNone    = "none"
	ProviderMomento = "momento"
	ProviderGA4     = "ga4"
	ProviderGTM     = "gtm"
	ProviderMatomo  = "matomo"
	ProviderCustom  = "custom"

	// SettingKey is the system_settings row that holds the configuration.
	SettingKey = "analytics.tracking"

	// MomentoProxyPath is where the application forwards collector traffic
	// when the same-origin proxy is on. The snippet then names no external
	// origin at all, which is what keeps the policy unchanged on a deployment
	// whose policy cannot be widened.
	MomentoProxyPath = "/momento"

	MaxSnippetBytes = 8 * 1024
)

// Providers lists the choices in the order the console shows them. Momento is
// first because it is the self-hosted collector: the only option where visit
// data never leaves the network.
var Providers = []string{ProviderMomento, ProviderGA4, ProviderGTM, ProviderMatomo, ProviderCustom, ProviderNone}

type Config struct {
	Enabled       bool   `json:"enabled"`
	Provider      string `json:"provider"`
	MomentoURL    string `json:"momento_url"`
	MomentoSiteID string `json:"momento_site_id"`
	MomentoProxy  bool   `json:"momento_proxy"`
	MeasurementID string `json:"measurement_id"`
	MatomoURL     string `json:"matomo_url"`
	MatomoSiteID  string `json:"matomo_site_id"`
	CustomSnippet string `json:"custom_snippet"`
	AllowedHosts  string `json:"allowed_hosts"`
	IncludeAdmin  bool   `json:"include_admin"`
	Placement     string `json:"placement"`
}

// Default is what a fresh installation stores: tracking off, Momento chosen
// through its same-origin proxy so that turning it on later needs nothing but
// a collector address and a site id.
func Default() Config {
	return Config{Provider: ProviderMomento, MomentoProxy: true, Placement: "head"}
}

// ReadConfig maps the stored setting object onto the configuration. Anything
// missing or of the wrong type keeps its default, so a partially edited
// setting degrades to "off" rather than to an error on every page.
func ReadConfig(values map[string]any) Config {
	config := Default()
	config.Enabled = boolValue(values, "enabled", false)
	config.Provider = strings.ToLower(stringValue(values, "provider", ProviderMomento))
	config.MomentoURL = stringValue(values, "momento_url", "")
	config.MomentoSiteID = stringValue(values, "momento_site_id", "")
	config.MomentoProxy = boolValue(values, "momento_proxy", true)
	config.MeasurementID = stringValue(values, "measurement_id", "")
	config.MatomoURL = stringValue(values, "matomo_url", "")
	config.MatomoSiteID = stringValue(values, "matomo_site_id", "")
	config.CustomSnippet = stringValue(values, "custom_snippet", "")
	config.AllowedHosts = stringValue(values, "allowed_hosts", "")
	config.IncludeAdmin = boolValue(values, "include_admin", false)
	config.Placement = strings.ToLower(stringValue(values, "placement", "head"))
	if config.Placement != "body" {
		config.Placement = "head"
	}
	return config
}

// Active reports whether a page should carry the snippet. Administrative
// pages are excluded unless an administrator asks for them, because console
// traffic is rarely the visitor data anybody wants.
func (c Config) Active(path string) bool {
	if !c.Enabled || c.Provider == ProviderNone || c.Provider == "" {
		return false
	}
	if !c.IncludeAdmin && (path == "/admin" || strings.HasPrefix(path, "/admin/")) {
		return false
	}
	return strings.TrimSpace(c.Snippet("")) != ""
}

// ProxyActive reports whether collector traffic on MomentoProxyPath should be
// forwarded. It is independent of Active so a page that was served before an
// administrator excluded it can still finish reporting.
func (c Config) ProxyActive() bool {
	return c.Enabled && c.Provider == ProviderMomento && c.MomentoProxy && originOf(c.MomentoURL) != ""
}

// Validate reports what is wrong with the configuration. Limits that protect
// the server apply whether or not tracking is on; what a provider needs is
// only demanded once the administrator turns it on, so a half-filled form can
// be saved and finished later.
func (c Config) Validate() error {
	if len(c.CustomSnippet) > MaxSnippetBytes {
		return fmt.Errorf("추적 코드는 %dKB를 넘을 수 없습니다", MaxSnippetBytes/1024)
	}
	if c.Placement != "head" && c.Placement != "body" {
		return fmt.Errorf("placement는 head 또는 body여야 합니다")
	}
	switch c.Provider {
	case ProviderNone, ProviderMomento, ProviderGA4, ProviderGTM, ProviderMatomo, ProviderCustom:
	default:
		return fmt.Errorf("provider는 %s 중 하나여야 합니다", strings.Join(Providers, ", "))
	}
	for _, address := range []string{c.MomentoURL, c.MatomoURL} {
		if strings.TrimSpace(address) != "" && originOf(address) == "" {
			return fmt.Errorf("수집기 주소는 http:// 또는 https://로 시작하는 올바른 URL이어야 합니다")
		}
	}
	if !c.Enabled {
		return nil
	}
	switch c.Provider {
	case ProviderMomento:
		if originOf(c.MomentoURL) == "" || strings.TrimSpace(c.MomentoSiteID) == "" {
			return fmt.Errorf("Momento 수집기 주소와 사이트 id가 필요합니다")
		}
	case ProviderGA4, ProviderGTM:
		if strings.TrimSpace(c.MeasurementID) == "" {
			return fmt.Errorf("측정 id가 필요합니다")
		}
	case ProviderMatomo:
		if originOf(c.MatomoURL) == "" || strings.TrimSpace(c.MatomoSiteID) == "" {
			return fmt.Errorf("Matomo 주소와 사이트 id가 필요합니다")
		}
	case ProviderCustom:
		if strings.TrimSpace(c.CustomSnippet) == "" {
			return fmt.Errorf("붙여넣은 추적 코드가 비어 있습니다")
		}
	}
	return nil
}

// Snippet renders the markup to inject. The nonce is applied to every script
// tag in the snippet so the policy can stay strict.
func (c Config) Snippet(nonce string) string {
	switch c.Provider {
	case ProviderMomento:
		site := html.EscapeString(strings.TrimSpace(c.MomentoSiteID))
		if site == "" {
			return ""
		}
		if c.MomentoProxy {
			if originOf(c.MomentoURL) == "" {
				return ""
			}
			return withNonce(fmt.Sprintf(`<script async src="%s/tracker.js" data-site-id="%s" data-environment="prd" data-contract-version="1" data-endpoint="%s"></script>`, MomentoProxyPath, site, MomentoProxyPath), nonce)
		}
		base := strings.TrimRight(strings.TrimSpace(c.MomentoURL), "/")
		if originOf(base) == "" {
			return ""
		}
		return withNonce(fmt.Sprintf(`<script async src="%s/tracker.js" data-site-id="%s" data-environment="prd" data-contract-version="1"></script>`, html.EscapeString(base), site), nonce)
	case ProviderGA4:
		id := html.EscapeString(strings.TrimSpace(c.MeasurementID))
		if id == "" {
			return ""
		}
		return withNonce(fmt.Sprintf(`<script async src="https://www.googletagmanager.com/gtag/js?id=%s"></script>
<script>window.dataLayer=window.dataLayer||[];function gtag(){dataLayer.push(arguments);}gtag('js',new Date());gtag('config','%s');</script>`, id, id), nonce)
	case ProviderGTM:
		id := html.EscapeString(strings.TrimSpace(c.MeasurementID))
		if id == "" {
			return ""
		}
		return withNonce(fmt.Sprintf(`<script>(function(w,d,s,l,i){w[l]=w[l]||[];w[l].push({'gtm.start':new Date().getTime(),event:'gtm.js'});var f=d.getElementsByTagName(s)[0],j=d.createElement(s),dl=l!='dataLayer'?'&l='+l:'';j.async=true;j.src='https://www.googletagmanager.com/gtm.js?id='+i+dl;f.parentNode.insertBefore(j,f);})(window,document,'script','dataLayer','%s');</script>`, id), nonce)
	case ProviderMatomo:
		base := strings.TrimRight(strings.TrimSpace(c.MatomoURL), "/")
		site := html.EscapeString(strings.TrimSpace(c.MatomoSiteID))
		if originOf(base) == "" || site == "" {
			return ""
		}
		return withNonce(fmt.Sprintf(`<script>var _paq=window._paq=window._paq||[];_paq.push(['trackPageView']);_paq.push(['enableLinkTracking']);(function(){var u="%s/";_paq.push(['setTrackerUrl',u+'matomo.php']);_paq.push(['setSiteId','%s']);var d=document,g=d.createElement('script'),s=d.getElementsByTagName('script')[0];g.async=true;g.src=u+'matomo.js';s.parentNode.insertBefore(g,s);})();</script>`, html.EscapeString(base), site), nonce)
	case ProviderCustom:
		return withNonce(strings.TrimSpace(c.CustomSnippet), nonce)
	}
	return ""
}

// withNonce adds the nonce to every script tag that does not already carry
// one, which is what lets a pasted snippet run under a strict policy unchanged.
func withNonce(snippet, nonce string) string {
	if nonce == "" || snippet == "" {
		return snippet
	}
	var builder strings.Builder
	remaining := snippet
	for {
		index := strings.Index(strings.ToLower(remaining), "<script")
		if index < 0 {
			builder.WriteString(remaining)
			return builder.String()
		}
		end := index + len("<script")
		builder.WriteString(remaining[:end])
		tag := remaining[end:]
		if closing := strings.Index(tag, ">"); closing >= 0 {
			tag = tag[:closing]
		}
		if !strings.Contains(strings.ToLower(tag), "nonce=") {
			builder.WriteString(` nonce="` + html.EscapeString(nonce) + `"`)
		}
		remaining = remaining[end:]
	}
}

// PolicySources lists the extra origins the snippet needs, derived from the
// provider so a common setup needs no policy knowledge at all. Momento behind
// the same-origin proxy contributes nothing: the browser only ever talks to
// this application.
func (c Config) PolicySources() (scripts []string, connects []string, images []string) {
	add := func(origin string) {
		scripts = append(scripts, origin)
		connects = append(connects, origin)
		images = append(images, origin)
	}
	switch c.Provider {
	case ProviderMomento:
		if !c.MomentoProxy {
			if origin := originOf(c.MomentoURL); origin != "" {
				add(origin)
			}
		}
	case ProviderGA4, ProviderGTM:
		scripts = append(scripts, "https://www.googletagmanager.com")
		connects = append(connects, "https://www.google-analytics.com", "https://analytics.google.com", "https://*.google-analytics.com")
		images = append(images, "https://www.google-analytics.com", "https://www.googletagmanager.com")
	case ProviderMatomo:
		if origin := originOf(c.MatomoURL); origin != "" {
			add(origin)
		}
	case ProviderCustom:
		// A pasted snippet names the addresses it loads and reports to, so
		// those origins are allowed without anybody reading a policy error.
		for _, origin := range SnippetOrigins(c.CustomSnippet) {
			add(origin)
		}
	}
	for _, host := range SplitHosts(c.AllowedHosts) {
		add(host)
	}
	return scripts, connects, images
}

// SplitHosts reads the administrator's allow list, which accepts commas,
// spaces or one entry per line.
func SplitHosts(list string) []string {
	var hosts []string
	for _, host := range strings.FieldsFunc(list, func(letter rune) bool {
		return letter == ',' || letter == ' ' || letter == '\n' || letter == '\r' || letter == '\t'
	}) {
		if trimmed := strings.TrimSuffix(strings.TrimSpace(host), "/"); trimmed != "" {
			hosts = append(hosts, trimmed)
		}
	}
	return hosts
}

// SnippetOrigins lists every http(s) origin written into a tracking snippet:
// the script it loads, the endpoint it posts to, the pixel it requests. A
// tracker almost always writes its own address somewhere in its loader, so
// reading them here is what keeps a pasted snippet working without the
// administrator translating a policy error into a host name.
func SnippetOrigins(snippet string) []string {
	origins := make([]string, 0, 2)
	seen := make(map[string]struct{}, 2)
	lowered := strings.ToLower(snippet)
	for index := 0; index < len(snippet); {
		start := strings.Index(lowered[index:], "http")
		if start < 0 {
			break
		}
		start += index
		end := start
		for end < len(snippet) && !isURLBoundary(snippet[end]) {
			end++
		}
		index = end
		origin := originOf(snippet[start:end])
		if origin == "" {
			continue
		}
		if _, duplicate := seen[origin]; duplicate {
			continue
		}
		seen[origin] = struct{}{}
		origins = append(origins, origin)
	}
	return origins
}

// isURLBoundary reports the characters that cannot appear in a URL written
// inside HTML or JavaScript, which is where each address ends.
func isURLBoundary(letter byte) bool {
	switch letter {
	case '"', '\'', '`', '<', '>', ' ', '\t', '\n', '\r', ')', ',', ';', '\\', '+':
		return true
	}
	return false
}

// originOf reduces an address to the scheme and host a policy names. Only
// http and https count: a data: or chrome-extension: address is neither
// something a policy can allow nor something a tracker needs.
func originOf(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" {
		return ""
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return ""
	}
	return scheme + "://" + strings.ToLower(parsed.Host)
}

func stringValue(values map[string]any, key, fallback string) string {
	if value, ok := values[key].(string); ok && strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return fallback
}

func boolValue(values map[string]any, key string, fallback bool) bool {
	if value, ok := values[key].(bool); ok {
		return value
	}
	return fallback
}
