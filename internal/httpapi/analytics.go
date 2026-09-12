package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/hkjang/Kkiit/internal/analytics"
	"github.com/hkjang/Kkiit/internal/cryptox"
)

// cspReportPath is where browsers post the requests the content security
// policy refused. It is unauthenticated because the browser sends the report
// on its own, and it stores nothing but a bounded list of origins in memory.
const cspReportPath = "/api/v1/analytics/csp-report"

// maxReportBytes keeps an unauthenticated endpoint from being used to push
// large bodies at the server.
const maxReportBytes = 8 * 1024

// analyticsCacheTTL matches the feature flag cache: a change made in the
// console reaches the next page within seconds without a query per asset.
const analyticsCacheTTL = 10 * time.Second

// nonceKey carries the per-request script nonce from the security headers to
// the page handler so the policy header and the injected snippet agree.
type nonceKey struct{}

func requestNonce(r *http.Request) string {
	value, _ := r.Context().Value(nonceKey{}).(string)
	return value
}

// isNonPagePath names what never renders in a browser tab: API, health,
// MCP and the collector proxy. These carry a policy that allows nothing at
// all, and the tracking snippet is never injected into them.
func isNonPagePath(path string) bool {
	return strings.HasPrefix(path, "/api/") || strings.HasPrefix(path, "/health/") || strings.HasPrefix(path, "/mcp") || path == analytics.MomentoProxyPath || strings.HasPrefix(path, analytics.MomentoProxyPath+"/")
}

// analyticsConfig reads the tracking settings. Failures are treated as "no
// tracking" so a settings outage never breaks the page.
func (s *Server) analyticsConfig(ctx context.Context) analytics.Config {
	s.analyticsMu.Lock()
	if s.analyticsCache != nil && time.Since(s.analyticsLoaded) < analyticsCacheTTL {
		cached := *s.analyticsCache
		s.analyticsMu.Unlock()
		return cached
	}
	s.analyticsMu.Unlock()
	loaded := analytics.Config{}
	if s.DB != nil {
		var raw []byte
		if err := s.DB.QueryRow(ctx, `SELECT value FROM system_settings WHERE key='analytics.tracking'`).Scan(&raw); err == nil {
			var values map[string]any
			if json.Unmarshal(raw, &values) == nil {
				loaded = analytics.ReadConfig(values)
			}
		}
	}
	s.analyticsMu.Lock()
	s.analyticsCache, s.analyticsLoaded = &loaded, time.Now()
	s.analyticsMu.Unlock()
	return loaded
}

func (s *Server) invalidateAnalytics() {
	s.analyticsMu.Lock()
	s.analyticsCache = nil
	s.analyticsMu.Unlock()
}

// basePolicy is the policy every page carried before tracking existed, and
// still carries while tracking is off. It has no 'unsafe-inline' for scripts
// and must never gain one: loosening it would let every inline script run,
// and would stay loose after tracking is turned off again.
const (
	basePolicy    = "default-src 'self'; script-src 'self'; img-src 'self' data: blob:; style-src 'self' 'unsafe-inline'; font-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'self'; frame-ancestors 'none'"
	nonPagePolicy = "default-src 'none'; frame-ancestors 'none'"
)

// pagePolicy keeps the strict page policy and adds only what the configured
// tracking snippet needs: a nonce for its inline code and the origins it
// loads from and reports to.
func pagePolicy(config analytics.Config, path, nonce string) string {
	if !config.Active(path) {
		return basePolicy
	}
	extraScripts, extraConnects, extraImages := config.PolicySources()
	scripts := append([]string{"'self'", "'nonce-" + nonce + "'"}, extraScripts...)
	images := append([]string{"'self'", "data:", "blob:"}, extraImages...)
	connects := append([]string{"'self'"}, extraConnects...)
	// While tracking is on, ask the browser to say what it refused. That
	// report is what turns a console error into a one-click fix.
	return "default-src 'self'; script-src " + strings.Join(scripts, " ") +
		"; img-src " + strings.Join(images, " ") +
		"; style-src 'self' 'unsafe-inline'; font-src 'self' data:; connect-src " + strings.Join(connects, " ") +
		"; object-src 'none'; base-uri 'self'; frame-ancestors 'none'; report-uri " + cspReportPath
}

// newNonce is the per-request value that ties the injected snippet to the
// policy header. When randomness fails the request goes out without a nonce,
// which the browser treats as "block the snippet" — the safe direction.
func newNonce() string {
	nonce, err := cryptox.RandomToken(16)
	if err != nil {
		return ""
	}
	return nonce
}

// injectSnippet places the markup just before the closing tag it belongs to,
// falling back to the end of the document when the tag is missing.
func injectSnippet(page []byte, snippet, placement string) []byte {
	marker := "</head>"
	if placement == "body" {
		marker = "</body>"
	}
	text := string(page)
	index := strings.LastIndex(strings.ToLower(text), marker)
	if index < 0 {
		return []byte(text + "\n" + snippet + "\n")
	}
	return []byte(text[:index] + snippet + "\n" + text[index:])
}

type cspReport struct {
	Report struct {
		BlockedURI         string `json:"blocked-uri"`
		ViolatedDirective  string `json:"violated-directive"`
		EffectiveDirective string `json:"effective-directive"`
		DocumentURI        string `json:"document-uri"`
	} `json:"csp-report"`
}

// receiveCSPReport records what a browser refused to load. Reports are always
// answered with 204 so a misbehaving page never sees an error from us.
func (s *Server) receiveCSPReport(w http.ResponseWriter, r *http.Request) {
	defer w.WriteHeader(http.StatusNoContent)
	body, err := io.ReadAll(io.LimitReader(r.Body, maxReportBytes))
	if err != nil || len(body) == 0 {
		return
	}
	var report cspReport
	if json.Unmarshal(body, &report) != nil {
		return
	}
	directive := report.Report.EffectiveDirective
	if directive == "" {
		directive = report.Report.ViolatedDirective
	}
	s.violations.Record(report.Report.BlockedURI, directive, report.Report.DocumentURI)
}

// listAnalyticsViolations shows the administrator which addresses the policy
// is blocking, so a tracking snippet can be fixed without reading the browser
// console.
func (s *Server) listAnalyticsViolations(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"items": s.violations.List(s.analyticsConfig(r.Context()))})
}

// clearAnalyticsViolations forgets the recorded reports, which is how an
// administrator checks whether a change actually fixed the snippet.
func (s *Server) clearAnalyticsViolations(w http.ResponseWriter, _ *http.Request) {
	s.violations.Forget()
	w.WriteHeader(http.StatusNoContent)
}

// allowAnalyticsHost adds one blocked origin to the tracking allow list. It is
// the one-click fix for the reports listed above, and goes through the same
// versioned, audited path as any other setting change.
func (s *Server) allowAnalyticsHost(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Origin string `json:"origin"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	origin := strings.TrimSpace(input.Origin)
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		writeError(w, http.StatusBadRequest, "invalid_origin", "허용할 주소는 http:// 또는 https://로 시작해야 합니다.")
		return
	}
	var raw []byte
	var version int64
	if err := s.DB.QueryRow(r.Context(), `SELECT value,version FROM system_settings WHERE key=$1`, analytics.SettingKey).Scan(&raw, &version); err != nil {
		writeError(w, http.StatusInternalServerError, "query_failed", "설정을 확인하지 못했습니다.")
		return
	}
	var values map[string]any
	if json.Unmarshal(raw, &values) != nil || values == nil {
		values = map[string]any{}
	}
	current, _ := values["allowed_hosts"].(string)
	values["allowed_hosts"] = analytics.AddAllowedHost(current, parsed.Scheme+"://"+parsed.Host)
	updated, err := json.Marshal(values)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "update_failed", "설정을 저장하지 못했습니다.")
		return
	}
	p, _ := principalFrom(r.Context())
	if _, err := s.DB.Exec(r.Context(), `UPDATE system_settings SET value=$2,version=version+1,updated_by=$3,updated_at=now() WHERE key=$1`, analytics.SettingKey, updated, p.UserID); err != nil {
		writeError(w, http.StatusInternalServerError, "update_failed", "설정을 저장하지 못했습니다.")
		return
	}
	s.invalidateAnalytics()
	s.audit(r, "settings.update", "system_setting", analytics.SettingKey, map[string]any{"allowed_hosts": current}, map[string]any{"allowed_hosts": values["allowed_hosts"]}, "success")
	writeJSON(w, http.StatusOK, map[string]any{"allowed_hosts": values["allowed_hosts"]})
}

// momentoProxy forwards collector traffic to the configured Momento address
// so the browser only ever talks to this origin. It is closed unless Momento
// is the active provider with the proxy turned on, and it forwards without
// the visitor's cookies: the collector has no business with the session.
func (s *Server) momentoProxy() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		config := s.analyticsConfig(r.Context())
		if !config.ProxyActive() {
			http.NotFound(w, r)
			return
		}
		target, err := url.Parse(strings.TrimRight(strings.TrimSpace(config.MomentoURL), "/"))
		if err != nil || target.Host == "" {
			http.NotFound(w, r)
			return
		}
		proxy := &httputil.ReverseProxy{
			Rewrite: func(request *httputil.ProxyRequest) {
				request.SetURL(target)
				request.Out.URL.Path = strings.TrimRight(target.Path, "/") + strings.TrimPrefix(r.URL.Path, analytics.MomentoProxyPath)
				request.Out.URL.RawPath = ""
				request.Out.Host = target.Host
				request.Out.Header.Del("Cookie")
				request.Out.Header.Del("Authorization")
				request.SetXForwarded()
			},
			ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
				s.Logger.Warn("momento proxy failed", "error", err, "path", r.URL.Path, "request_id", requestIDFrom(r.Context()))
				w.WriteHeader(http.StatusBadGateway)
			},
		}
		proxy.ServeHTTP(w, r)
	})
}
