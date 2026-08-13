package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hkjang/Kkiit/internal/cryptox"
)

type Server struct {
	DB               *pgxpool.Pool
	Box              *cryptox.Box
	Version          string
	Commit           string
	BuiltAt          string
	Logger           *slog.Logger
	rateMu           sync.Mutex
	rate             map[uuid.UUID]rateWindow
	hubMu            sync.Mutex
	orderSubscribers map[uuid.UUID]map[chan []byte]struct{}
}

type rateWindow struct {
	started time.Time
	count   int
}

type Principal struct {
	UserID      uuid.UUID  `json:"id"`
	Username    string     `json:"username"`
	Email       *string    `json:"email,omitempty"`
	DisplayName string     `json:"display_name"`
	Roles       []string   `json:"roles"`
	Permissions []string   `json:"permissions"`
	SessionID   *uuid.UUID `json:"-"`
	APIKeyID    *uuid.UUID `json:"-"`
}

type contextKey string

const (
	principalKey contextKey = "principal"
	requestIDKey contextKey = "requestID"
)

func principalFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey).(Principal)
	return p, ok
}

func requestIDFrom(ctx context.Context) string {
	v, _ := ctx.Value(requestIDKey).(string)
	return v
}

type apiError struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if value != nil {
		_ = json.NewEncoder(w).Encode(value)
	}
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": apiError{Code: code, Message: message}})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "요청 본문을 확인해 주세요.")
		return false
	}
	return true
}

func hasPermission(p Principal, permission string) bool {
	for _, item := range p.Permissions {
		if item == permission {
			return true
		}
	}
	return false
}

func (s *Server) require(permission string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, ok := principalFrom(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "authentication_required", "로그인이 필요합니다.")
			return
		}
		if permission != "" && !hasPermission(p, permission) {
			writeError(w, http.StatusForbidden, "permission_denied", "이 작업을 수행할 권한이 없습니다.")
			return
		}
		next(w, r)
	}
}

func splitCSV(value string) []string {
	if strings.TrimSpace(value) == "" {
		return []string{}
	}
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func parseUUIDPath(w http.ResponseWriter, r *http.Request, name string) (uuid.UUID, bool) {
	value, err := uuid.Parse(r.PathValue(name))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "올바르지 않은 식별자입니다.")
		return uuid.Nil, false
	}
	return value, true
}

func nullableTime(t *time.Time) any {
	if t == nil || t.IsZero() {
		return nil
	}
	return *t
}

var errNotFound = errors.New("not found")
