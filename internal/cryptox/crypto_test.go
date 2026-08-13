package cryptox

import (
	"bytes"
	"testing"
)

func TestBoxRoundTripAndPurposeBinding(t *testing.T) {
	box, err := New(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := box.Encrypt([]byte("client-secret"), "provider:one")
	if err != nil {
		t.Fatal(err)
	}
	plain, err := box.Decrypt(ciphertext, "provider:one")
	if err != nil {
		t.Fatal(err)
	}
	if string(plain) != "client-secret" {
		t.Fatalf("plain=%q", plain)
	}
	if _, err := box.Decrypt(ciphertext, "provider:two"); err == nil {
		t.Fatal("purpose mismatch must fail")
	}
}

func TestDigestMatches(t *testing.T) {
	if !DigestMatches(Digest("token"), "token") {
		t.Fatal("digest should match")
	}
	if DigestMatches(Digest("token"), "other") {
		t.Fatal("digest should not match")
	}
}
