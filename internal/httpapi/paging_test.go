package httpapi

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestCursorRoundTrip(t *testing.T) {
	at := time.Date(2026, 8, 25, 12, 34, 56, 789, time.UTC)
	id := uuid.New()
	decoded, ok := decodeCursor(encodeCursor(at, id))
	if !ok {
		t.Fatal("직접 만든 커서를 되읽지 못했습니다")
	}
	if !decoded.At.Equal(at) || decoded.ID != id {
		t.Fatalf("커서 왕복 결과=%v/%v", decoded.At, decoded.ID)
	}
}

// A stale or tampered bookmark must fall back to the newest page rather than
// failing the request.
func TestBadCursorsStartFromTheTop(t *testing.T) {
	for _, raw := range []string{"", "not-base64!!", "Zm9v", "MTIzNDo=", "MTIzNDpub3QtYS11dWlk"} {
		if _, ok := decodeCursor(raw); ok {
			t.Fatalf("%q는 커서로 받아들이면 안 됩니다", raw)
		}
	}
	request := httptest.NewRequest("GET", "/api/v1/admin/audit?cursor=broken", nil)
	if at, id := requestCursor(request); at != nil || id != nil {
		t.Fatalf("잘못된 커서가 조건으로 넘어갔습니다: %v/%v", at, id)
	}
}

func TestPageResultTrimsAndReportsTheNextCursor(t *testing.T) {
	base := time.Date(2026, 8, 25, 0, 0, 0, 0, time.UTC)
	rows := make([]map[string]any, 0, 4)
	for i := 0; i < 4; i++ {
		rows = append(rows, map[string]any{"created_at": base.Add(-time.Duration(i) * time.Hour), "id": uuid.New()})
	}
	items, next := pageResult(rows, 3, func(item map[string]any) (time.Time, uuid.UUID) {
		return timeField(item, "created_at"), uuidField(item, "id")
	})
	if len(items) != 3 {
		t.Fatalf("페이지 크기=%d", len(items))
	}
	cursor, ok := decodeCursor(next.(string))
	if !ok {
		t.Fatalf("다음 커서=%v", next)
	}
	// The cursor must point at the last returned row so the next page starts
	// exactly after it, with no gap and no repeat.
	if !cursor.At.Equal(timeField(items[2], "created_at")) || cursor.ID != uuidField(items[2], "id") {
		t.Fatal("커서가 마지막 항목을 가리키지 않습니다")
	}

	short, done := pageResult(rows[:2], 3, func(item map[string]any) (time.Time, uuid.UUID) {
		return timeField(item, "created_at"), uuidField(item, "id")
	})
	if len(short) != 2 || done != nil {
		t.Fatalf("마지막 페이지=%d건 커서=%v", len(short), done)
	}
}

func TestThrottleCountsPerCallerAndWindow(t *testing.T) {
	server := &Server{}
	for i := 0; i < 3; i++ {
		if !server.allow("coupon_preview:user:a", 3, time.Minute) {
			t.Fatalf("한도 안의 %d번째 요청이 거부되었습니다", i+1)
		}
	}
	if server.allow("coupon_preview:user:a", 3, time.Minute) {
		t.Fatal("한도를 넘긴 요청이 통과했습니다")
	}
	if !server.allow("coupon_preview:user:b", 3, time.Minute) {
		t.Fatal("다른 사용자가 함께 막혔습니다")
	}
	if !server.allow("report_create:user:a", 3, time.Minute) {
		t.Fatal("다른 엔드포인트가 함께 막혔습니다")
	}
	if server.allow("coupon_preview:user:c", 0, time.Minute) {
		t.Fatal("한도 0은 모두 거부해야 합니다")
	}
}

func TestThrottleDefaultsCoverTheRoutedEndpoints(t *testing.T) {
	for _, name := range []string{"coupon_preview", "report_create", "dispute_open", "webhook_test", "order_create", "message_create"} {
		if throttleDefaults[name] <= 0 {
			t.Fatalf("%s에 기본 한도가 없습니다", name)
		}
	}
}
