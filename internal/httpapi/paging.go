package httpapi

import (
	"encoding/base64"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Keyset paging beats OFFSET here because these lists grow forever and are
// almost always read newest first. The cursor carries the sort key and the row
// identity so rows inserted while a reader pages through cannot shift the
// window or make a row appear twice.
type pageCursor struct {
	At time.Time
	ID uuid.UUID
}

func encodeCursor(at time.Time, id uuid.UUID) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(at.UTC().UnixNano(), 10) + ":" + id.String()))
}

func decodeCursor(raw string) (pageCursor, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return pageCursor{}, false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return pageCursor{}, false
	}
	parts := strings.SplitN(string(decoded), ":", 2)
	if len(parts) != 2 {
		return pageCursor{}, false
	}
	nanos, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return pageCursor{}, false
	}
	id, err := uuid.Parse(parts[1])
	if err != nil {
		return pageCursor{}, false
	}
	return pageCursor{At: time.Unix(0, nanos).UTC(), ID: id}, true
}

// requestCursor returns the SQL arguments for the keyset predicate. A missing
// or malformed cursor starts from the newest row rather than failing, so a
// stale bookmark degrades to a fresh page instead of an error.
func requestCursor(r *http.Request) (any, any) {
	cursor, ok := decodeCursor(r.URL.Query().Get("cursor"))
	if !ok {
		return nil, nil
	}
	return cursor.At, cursor.ID
}

// pageResult trims the extra row used to detect a further page and returns the
// cursor a caller should send to continue.
func pageResult(items []map[string]any, limit int, at func(map[string]any) (time.Time, uuid.UUID)) ([]map[string]any, any) {
	if len(items) <= limit {
		return items, nil
	}
	items = items[:limit]
	last := items[len(items)-1]
	when, id := at(last)
	return items, encodeCursor(when, id)
}

func timeField(item map[string]any, key string) time.Time {
	if value, ok := item[key].(time.Time); ok {
		return value
	}
	return time.Time{}
}

func uuidField(item map[string]any, key string) uuid.UUID {
	if value, ok := item[key].(uuid.UUID); ok {
		return value
	}
	return uuid.Nil
}
