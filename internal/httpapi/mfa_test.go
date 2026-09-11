package httpapi

import (
	"encoding/base32"
	"testing"
	"time"
)

func TestTOTPVerificationAndDrift(t *testing.T) {
	secretBytes := []byte("12345678901234567890")
	secret := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(secretBytes)
	at := time.Unix(1_234_567_890, 0)
	code := totpCode(secretBytes, uint64(at.Unix()/30))
	if !verifyTOTP(secret, code, at) {
		t.Fatal("current TOTP must verify")
	}
	if !verifyTOTP(secret, code, at.Add(30*time.Second)) {
		t.Fatal("one period of clock drift must verify")
	}
	if verifyTOTP(secret, "000000", at) && code != "000000" {
		t.Fatal("unrelated code must not verify")
	}
}

// A code that is still inside its validity window could be presented twice, so
// the step it belongs to is what the caller records to spend it.
func TestMatchTOTPStepIdentifiesTheWindow(t *testing.T) {
	secret := "JBSWY3DPEHPK3PXP"
	now := time.Unix(1_700_000_000, 0)
	step := now.Unix() / 30
	code := totpCode(mustDecodeBase32(t, secret), uint64(step))

	matched, ok := matchTOTPStep(secret, code, now)
	if !ok || matched != step {
		t.Fatalf("현재 코드 step=%d ok=%v (기대 %d)", matched, ok, step)
	}
	// The same code still verifies a moment later, which is exactly why the
	// consumed step has to be remembered rather than trusting freshness.
	matched, ok = matchTOTPStep(secret, code, now.Add(20*time.Second))
	if !ok || matched != step {
		t.Fatalf("같은 창 안의 재검증 step=%d ok=%v", matched, ok)
	}
	if _, ok := matchTOTPStep(secret, code, now.Add(5*time.Minute)); ok {
		t.Fatal("창을 벗어난 코드가 통과했습니다")
	}
	if _, ok := matchTOTPStep(secret, "000000", now); ok {
		if totpCode(mustDecodeBase32(t, secret), uint64(step)) != "000000" {
			t.Fatal("임의의 코드가 통과했습니다")
		}
	}
	if _, ok := matchTOTPStep(secret, "12345", now); ok {
		t.Fatal("6자리가 아닌 코드가 통과했습니다")
	}
}

func mustDecodeBase32(t *testing.T, secret string) []byte {
	t.Helper()
	decoded, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	if err != nil {
		t.Fatalf("secret decode: %v", err)
	}
	return decoded
}
