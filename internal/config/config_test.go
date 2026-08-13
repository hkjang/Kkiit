package config

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestLoadAcceptsExactlyTheBootstrapContract(t *testing.T) {
	t.Setenv("POSTGRES_DSN", "postgres://test")
	t.Setenv("BOOTSTRAP_ADMIN", "admin@example.test")
	t.Setenv("BOOTSTRAP_ADMIN_PASSWORD", "a-secure-password")
	t.Setenv("ENCRYPTION_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32))))
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if len(cfg.EncryptionKey) != 32 {
		t.Fatalf("key length=%d", len(cfg.EncryptionKey))
	}
}

func TestLoadRejectsWeakBootstrapPassword(t *testing.T) {
	t.Setenv("POSTGRES_DSN", "postgres://test")
	t.Setenv("BOOTSTRAP_ADMIN", "admin")
	t.Setenv("BOOTSTRAP_ADMIN_PASSWORD", "short")
	t.Setenv("ENCRYPTION_KEY", strings.Repeat("01", 32))
	if _, err := Load(); err == nil {
		t.Fatal("expected weak password error")
	}
}

func TestEncryptionKeyMustBe32Bytes(t *testing.T) {
	if _, err := parseEncryptionKey(base64.StdEncoding.EncodeToString([]byte("too short"))); err == nil {
		t.Fatal("expected key length error")
	}
}
