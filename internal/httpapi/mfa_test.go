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
