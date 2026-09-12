package httpapi

import (
	"net/http"
	"time"

	"github.com/google/uuid"
)

// throttleDefaults are per minute ceilings for endpoints that are cheap to call
// and expensive to abuse. Coupon preview is the strictest because it is the one
// place a signed in buyer can test arbitrary codes.
var throttleDefaults = map[string]int{
	"coupon_preview":  10,
	"report_create":   10,
	"dispute_open":    10,
	"webhook_test":    10,
	"order_create":    30,
	"message_create":  60,
	"password_change": 5,
	"login":           10,
	"talent_view":     120,
	"inquiry_create":  20,
	"csp_report":      60,
}

// allow is a fixed window counter shared by API key limits and per endpoint
// throttles. A fixed window can let a burst straddle the boundary, which is an
// acceptable trade for a counter that needs no background sweeping.
func (s *Server) allow(key string, limit int, window time.Duration) bool {
	if limit <= 0 {
		return false
	}
	now := time.Now()
	s.rateMu.Lock()
	defer s.rateMu.Unlock()
	if s.rate == nil {
		s.rate = make(map[string]rateWindow)
	}
	if len(s.rate) > 20000 {
		// Bound the map instead of growing it for every caller ever seen.
		for name, entry := range s.rate {
			if now.Sub(entry.started) >= window {
				delete(s.rate, name)
			}
		}
	}
	entry := s.rate[key]
	if entry.started.IsZero() || now.Sub(entry.started) >= window {
		s.rate[key] = rateWindow{started: now, count: 1}
		return true
	}
	if entry.count >= limit {
		return false
	}
	entry.count++
	s.rate[key] = entry
	return true
}

func (s *Server) allowAPIKey(id uuid.UUID, limit int) bool {
	return s.allow("key:"+id.String(), limit, time.Minute)
}

// throttle limits one endpoint per caller. Signed in callers are counted by
// account so sharing an office IP does not make one person's activity block
// everyone else's.
func (s *Server) throttle(name string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit := throttleDefaults[name]
		if policy, err := s.settingObject(r, "api.policy"); err == nil {
			if limits, ok := policy["throttle_per_minute"].(map[string]any); ok {
				if value, ok := limits[name].(float64); ok && value >= 1 && value <= 100000 {
					limit = int(value)
				}
			}
		}
		if limit <= 0 {
			next(w, r)
			return
		}
		caller := "ip:" + clientIP(r).String()
		if p, ok := principalFrom(r.Context()); ok && p.UserID != uuid.Nil {
			caller = "user:" + p.UserID.String()
		}
		if !s.allow(name+":"+caller, limit, time.Minute) {
			w.Header().Set("Retry-After", "60")
			writeError(w, http.StatusTooManyRequests, "rate_limit_exceeded", "요청이 너무 잦습니다. 잠시 후 다시 시도해 주세요.")
			return
		}
		next(w, r)
	}
}
