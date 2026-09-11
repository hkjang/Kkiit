package worker

import (
	"testing"
	"time"
)

func TestRenderSubstitutesKnownPlaceholdersAndDropsUnknownOnes(t *testing.T) {
	variables := map[string]string{"order_number": "KK-1", "talent_title": "Go 성능 점검"}
	if got := render("{{talent_title}} 주문 {{order_number}}이 접수되었습니다.", variables); got != "Go 성능 점검 주문 KK-1이 접수되었습니다." {
		t.Fatalf("render=%q", got)
	}
	if got := render("{{missing}} 알림", variables); got != "알림" {
		t.Fatalf("unknown placeholder must collapse, got %q", got)
	}
	if got := render("열린 괄호 {{order_number", variables); got != "열린 괄호 {{order_number" {
		t.Fatalf("unterminated placeholder must stay literal, got %q", got)
	}
}

func TestGroupDigitsFormatsAmountsForTemplates(t *testing.T) {
	for _, testCase := range []struct {
		value int64
		want  string
	}{{0, "0"}, {999, "999"}, {1000, "1,000"}, {1234567, "1,234,567"}, {-45000, "-45,000"}} {
		if got := groupDigits(testCase.value); got != testCase.want {
			t.Fatalf("groupDigits(%d)=%q want %q", testCase.value, got, testCase.want)
		}
	}
}

func TestBackoffGrowsAndStaysBounded(t *testing.T) {
	if got := backoff(1, time.Minute, 6*time.Hour); got != time.Minute {
		t.Fatalf("first attempt backoff=%s", got)
	}
	if got := backoff(3, time.Minute, 6*time.Hour); got != 4*time.Minute {
		t.Fatalf("third attempt backoff=%s", got)
	}
	if got := backoff(40, time.Minute, 6*time.Hour); got != 6*time.Hour {
		t.Fatalf("large attempt must clamp to the max, got %s", got)
	}
}

func TestPolicySettingsRejectOutOfRangeValues(t *testing.T) {
	source := map[string]any{"event_batch": float64(9000), "poll_seconds": float64(0), "enabled": false}
	if got := intSetting(source, "event_batch", 50, 1, 500); got != 50 {
		t.Fatalf("out of range int must fall back, got %d", got)
	}
	if got := durationSetting(source, "poll_seconds", time.Second, 2*time.Second, time.Second, time.Minute); got != 2*time.Second {
		t.Fatalf("out of range duration must fall back, got %s", got)
	}
	if boolSetting(source, "enabled", true) {
		t.Fatal("explicit false must win over the fallback")
	}
	if !boolSetting(source, "absent", true) {
		t.Fatal("missing key must use the fallback")
	}
}

func TestScalarTextOnlyAcceptsRenderableValues(t *testing.T) {
	if text, ok := scalarText(float64(1500000)); !ok || text != "1,500,000" {
		t.Fatalf("integer payload value=%q ok=%v", text, ok)
	}
	if _, ok := scalarText(map[string]any{"nested": true}); ok {
		t.Fatal("nested objects must not be exposed as template variables")
	}
}

func TestParseUUIDValueRejectsNonUUIDPayloads(t *testing.T) {
	if _, ok := parseUUIDValue("not-a-uuid"); ok {
		t.Fatal("malformed actor must not parse")
	}
	if _, ok := parseUUIDValue("00000000-0000-0000-0000-000000000000"); ok {
		t.Fatal("nil UUID must not be treated as an actor")
	}
	if _, ok := parseUUIDValue("2f1c8d84-4d4f-4a7d-9f24-1a4b7c2d9e01"); !ok {
		t.Fatal("valid UUID must parse")
	}
}
