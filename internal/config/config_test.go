package config

import (
	"encoding/base64"
	"os"
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

func TestLoadShutdownDrainSeconds(t *testing.T) {
	t.Setenv("POSTGRES_DSN", "postgres://test")
	t.Setenv("BOOTSTRAP_ADMIN", "admin@example.test")
	t.Setenv("BOOTSTRAP_ADMIN_PASSWORD", "a-secure-password")
	t.Setenv("ENCRYPTION_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32))))

	tests := []struct {
		name  string
		value string
		unset bool
		want  int
	}{
		{name: "unset", unset: true, want: 5},
		{name: "empty", value: "", want: 5},
		{name: "whitespace_only", value: " \t\n", want: 5},
		{name: "negative", value: "-1", want: 5},
		{name: "above_maximum", value: "121", want: 5},
		{name: "non_integer", value: "abc", want: 5},
		{name: "fractional", value: "1.5", want: 5},
		{name: "integer_overflow", value: "99999999999999999999999999999999999999", want: 5},
		{name: "zero", value: "0", want: 0},
		{name: "one", value: "1", want: 1},
		{name: "default_value", value: "5", want: 5},
		{name: "maximum", value: "120", want: 120},
		{name: "surrounding_whitespace", value: " 12 \t", want: 12},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setenv registers cleanup even when this case needs the variable absent.
			t.Setenv("SHUTDOWN_DRAIN_SECONDS", tt.value)
			if tt.unset {
				if err := os.Unsetenv("SHUTDOWN_DRAIN_SECONDS"); err != nil {
					t.Fatalf("Unsetenv(): %v", err)
				}
			}

			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load(): %v", err)
			}
			if cfg.DrainSeconds != tt.want {
				t.Errorf("DrainSeconds=%d, want %d", cfg.DrainSeconds, tt.want)
			}
		})
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
