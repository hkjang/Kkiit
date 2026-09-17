package httpapi

import (
	"bufio"
	"context"
	"crypto/subtle"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"runtime/debug"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/hkjang/Kkiit/internal/cryptox"
)

func (s *Server) middleware(next http.Handler) http.Handler {
	return s.recoverer(s.requestContext(s.securityHeaders(s.authentication(s.csrf(next)))))
}

func (s *Server) csrf(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		safe := r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions
		_, cookieErr := r.Cookie("kkiit_session")
		hasSessionCookie := cookieErr == nil
		// A policy violation report is posted by the browser itself, not by
		// the page, and records nothing but an origin. It is let through so
		// an administrator's own blocked snippet is the first one reported.
		if safe || !hasSessionCookie || r.URL.Path == cspReportPath {
			next.ServeHTTP(w, r)
			return
		}
		if strings.EqualFold(r.Header.Get("Sec-Fetch-Site"), "cross-site") {
			writeError(w, http.StatusForbidden, "csrf_rejected", "교차 사이트 요청이 차단되었습니다.")
			return
		}
		if origin := strings.TrimSpace(r.Header.Get("Origin")); origin != "" {
			parsed, err := url.Parse(origin)
			if err != nil || !strings.EqualFold(parsed.Host, r.Host) {
				writeError(w, http.StatusForbidden, "csrf_rejected", "요청 출처를 확인할 수 없습니다.")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// recorder remembers what was answered. It forwards Hijack and Flush because
// the message socket upgrades the connection and would break behind a wrapper
// that swallowed them.
type recorder struct {
	http.ResponseWriter
	status  int
	written int64
}

func (rec *recorder) WriteHeader(status int) {
	if rec.status == 0 {
		rec.status = status
	}
	rec.ResponseWriter.WriteHeader(status)
}

func (rec *recorder) Write(data []byte) (int, error) {
	if rec.status == 0 {
		rec.status = http.StatusOK
	}
	n, err := rec.ResponseWriter.Write(data)
	rec.written += int64(n)
	return n, err
}

func (rec *recorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := rec.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, http.ErrNotSupported
	}
	return hijacker.Hijack()
}

// Unwrap is what http.ResponseController follows to reach the connection
// deadlines the message socket clears. Without it the wrapper silently blocks
// that, and every WebSocket would be cut at the server's write timeout instead
// of staying open.
func (rec *recorder) Unwrap() http.ResponseWriter {
	return rec.ResponseWriter
}

func (rec *recorder) Flush() {
	if flusher, ok := rec.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (s *Server) requestContext(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := strings.TrimSpace(r.Header.Get("X-Request-ID"))
		if requestID == "" || len(requestID) > 128 {
			requestID = uuid.NewString()
		}
		w.Header().Set("X-Request-ID", requestID)
		started := time.Now()
		rec := &recorder{ResponseWriter: w}
		next.ServeHTTP(rec, r.WithContext(context.WithValue(r.Context(), requestIDKey, requestID)))
		status := rec.status
		if status == 0 {
			status = http.StatusOK
		}
		// Every request used to be one indistinguishable INFO line with no
		// status, so a failure could not be found in the log at all, and the
		// signal was buried under one line per asset on every page load.
		// Failures are now loud and successful static reads are quiet.
		level := slog.LevelInfo
		switch {
		case status >= 500:
			level = slog.LevelError
		case status >= 400:
			level = slog.LevelWarn
		case !strings.HasPrefix(r.URL.Path, "/api/") && !strings.HasPrefix(r.URL.Path, "/mcp"):
			level = slog.LevelDebug
		}
		s.Logger.Log(r.Context(), level, "http request", "method", r.Method, "path", r.URL.Path,
			"status", status, "bytes", rec.written, "request_id", requestID,
			"duration_ms", time.Since(started).Milliseconds())
	})
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		if isNonPagePath(r.URL.Path) {
			w.Header().Set("Content-Security-Policy", nonPagePolicy)
			next.ServeHTTP(w, r)
			return
		}
		// Pages get a fresh nonce on every request. The policy names it and
		// the page handler writes the same value into the tracking snippet,
		// which is how inline tracking code runs without 'unsafe-inline'.
		nonce := newNonce()
		r = r.WithContext(context.WithValue(r.Context(), nonceKey{}, nonce))
		w.Header().Set("Content-Security-Policy", pagePolicy(s.analyticsConfig(r.Context()), r.URL.Path, nonce))
		next.ServeHTTP(w, r)
	})
}

func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				s.Logger.Error("panic recovered", "error", recovered, "stack", string(debug.Stack()), "request_id", requestIDFrom(r.Context()))
				writeError(w, http.StatusInternalServerError, "internal_error", "요청을 처리하지 못했습니다.")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (s *Server) authentication(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var principal Principal
		var found bool
		if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
			token := strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
			// One header, two kinds of credential. A key is a key wherever
			// it is sent. A JWT is an SSO access token, and it opens the MCP
			// endpoint only — see mcpoauth.go, which also says why when it
			// refuses. Anywhere else it is what it always was: not a key.
			if r.URL.Path == mcpPath && !strings.HasPrefix(token, "kkiit_") && looksLikeJWT(token) {
				var refusal string
				principal, found, refusal = s.authenticateMCPOAuth(r, token)
				if refusal != "" {
					r = r.WithContext(context.WithValue(r.Context(), mcpOAuthRefusalKey, refusal))
				}
			} else {
				var limited bool
				principal, found, limited = s.authenticateAPIKey(r, token)
				if limited {
					w.Header().Set("Retry-After", "60")
					writeError(w, http.StatusTooManyRequests, "rate_limit_exceeded", "API 키의 분당 요청 한도를 초과했습니다.")
					return
				}
			}
		} else if cookie, err := r.Cookie("kkiit_session"); err == nil {
			principal, found = s.authenticateSession(r, cookie.Value)
		}
		if found {
			r = r.WithContext(context.WithValue(r.Context(), principalKey, principal))
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) authenticateSession(r *http.Request, token string) (Principal, bool) {
	var p Principal
	var sessionID uuid.UUID
	var roles, permissions []string
	err := s.DB.QueryRow(r.Context(), `
		SELECT u.id,u.username,u.email,u.display_name,se.id,
		       COALESCE(array_agg(DISTINCT ur.role_code) FILTER (WHERE ur.role_code IS NOT NULL),ARRAY[]::text[]),
		       COALESCE(array_agg(DISTINCT rp.permission_code) FILTER (WHERE rp.permission_code IS NOT NULL),ARRAY[]::text[])
		FROM sessions se JOIN users u ON u.id=se.user_id
		LEFT JOIN user_roles ur ON ur.user_id=u.id
		LEFT JOIN role_permissions rp ON rp.role_code=ur.role_code
		WHERE se.token_hash=$1 AND se.revoked_at IS NULL AND se.expires_at > now() AND u.status='active'
		GROUP BY u.id,se.id`, cryptox.Digest(token)).Scan(&p.UserID, &p.Username, &p.Email, &p.DisplayName, &sessionID, &roles, &permissions)
	if err != nil {
		return Principal{}, false
	}
	p.Roles, p.Permissions, p.SessionID = roles, permissions, &sessionID
	_, _ = s.DB.Exec(r.Context(), `UPDATE sessions SET last_seen_at=now() WHERE id=$1 AND last_seen_at < now()-interval '5 minutes'`, sessionID)
	return p, true
}

func (s *Server) authenticateAPIKey(r *http.Request, token string) (Principal, bool, bool) {
	if !strings.HasPrefix(token, "kkiit_") || len(token) < 32 {
		return Principal{}, false, false
	}
	var p Principal
	var keyID uuid.UUID
	var keyScopes, roles, rolePermissions []string
	var storedHash []byte
	var allowedCIDRs []string
	var rateLimit int
	err := s.DB.QueryRow(r.Context(), `
		SELECT u.id,u.username,u.email,u.display_name,k.id,k.secret_hash,k.scopes,k.allowed_cidrs::text[],k.rate_limit_per_minute,
		       COALESCE(array_agg(DISTINCT ur.role_code) FILTER (WHERE ur.role_code IS NOT NULL),ARRAY[]::text[]),
		       COALESCE(array_agg(DISTINCT rp.permission_code) FILTER (WHERE rp.permission_code IS NOT NULL),ARRAY[]::text[])
		FROM api_keys k JOIN users u ON u.id=k.user_id
		LEFT JOIN user_roles ur ON ur.user_id=u.id
		LEFT JOIN role_permissions rp ON rp.role_code=ur.role_code
		WHERE k.secret_hash=$1 AND k.revoked_at IS NULL AND (k.expires_at IS NULL OR k.expires_at > now()) AND u.status='active'
		GROUP BY u.id,k.id`, cryptox.Digest(token)).Scan(&p.UserID, &p.Username, &p.Email, &p.DisplayName, &keyID, &storedHash, &keyScopes, &allowedCIDRs, &rateLimit, &roles, &rolePermissions)
	if err != nil || subtle.ConstantTimeCompare(storedHash, cryptox.Digest(token)) != 1 || !ipAllowed(clientIP(r), allowedCIDRs) {
		return Principal{}, false, false
	}
	if !s.allowAPIKey(keyID, rateLimit) {
		return Principal{}, false, true
	}
	allowed := make(map[string]bool, len(rolePermissions))
	for _, permission := range rolePermissions {
		allowed[permission] = true
	}
	permissions := make([]string, 0, len(keyScopes))
	for _, scope := range keyScopes {
		if allowed[scope] {
			permissions = append(permissions, scope)
		}
	}
	p.Roles, p.Permissions, p.APIKeyID = roles, permissions, &keyID
	_, _ = s.DB.Exec(r.Context(), `UPDATE api_keys SET last_used_at=now() WHERE id=$1 AND (last_used_at IS NULL OR last_used_at < now()-interval '5 minutes')`, keyID)
	return p, true, false
}

func ipAllowed(ip net.IP, cidrs []string) bool {
	if len(cidrs) == 0 {
		return true
	}
	if ip == nil {
		return false
	}
	for _, raw := range cidrs {
		_, network, err := net.ParseCIDR(raw)
		if err == nil && network.Contains(ip) {
			return true
		}
	}
	return false
}

func clientIP(r *http.Request) net.IP {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return net.ParseIP(host)
}

func (s *Server) audit(r *http.Request, action, resourceType, resourceID string, before, after any, result string) {
	p, _ := principalFrom(r.Context())
	var beforeJSON, afterJSON any
	if before != nil {
		beforeJSON = before
	}
	if after != nil {
		afterJSON = after
	}
	var sessionID any
	if p.SessionID != nil {
		sessionID = *p.SessionID
	}
	roles := p.Roles
	if roles == nil {
		roles = []string{}
	}
	_, err := s.DB.Exec(r.Context(), `INSERT INTO audit_logs(id,actor_user_id,actor_roles,ip_address,user_agent,action,resource_type,resource_id,before_data,after_data,request_id,session_id,result)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`, uuid.New(), nullableUUID(p.UserID), roles, clientIP(r), r.UserAgent(), action, resourceType, nullableString(resourceID), beforeJSON, afterJSON, requestIDFrom(r.Context()), sessionID, result)
	if err != nil {
		s.Logger.Error("audit write failed", slog.String("error", err.Error()), slog.String("action", action))
	}
}

func nullableUUID(id uuid.UUID) any {
	if id == uuid.Nil {
		return nil
	}
	return id
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

var _ = pgx.ErrNoRows
