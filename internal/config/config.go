package config

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
)

// Config intentionally exposes only the four bootstrap settings allowed by the
// deployment contract. Every mutable operating setting lives in PostgreSQL.
type Config struct {
	PostgresDSN            string
	BootstrapAdmin         string
	BootstrapAdminPassword string
	EncryptionKey          []byte
}

func Load() (Config, error) {
	key, err := parseEncryptionKey(os.Getenv("ENCRYPTION_KEY"))
	if err != nil {
		return Config{}, err
	}
	cfg := Config{
		PostgresDSN:            strings.TrimSpace(os.Getenv("POSTGRES_DSN")),
		BootstrapAdmin:         strings.TrimSpace(os.Getenv("BOOTSTRAP_ADMIN")),
		BootstrapAdminPassword: os.Getenv("BOOTSTRAP_ADMIN_PASSWORD"),
		EncryptionKey:          key,
	}
	if cfg.PostgresDSN == "" || cfg.BootstrapAdmin == "" || cfg.BootstrapAdminPassword == "" {
		return Config{}, errors.New("POSTGRES_DSN, BOOTSTRAP_ADMIN, BOOTSTRAP_ADMIN_PASSWORD and ENCRYPTION_KEY are required")
	}
	if len(cfg.BootstrapAdminPassword) < 12 {
		return Config{}, errors.New("BOOTSTRAP_ADMIN_PASSWORD must be at least 12 characters")
	}
	return cfg, nil
}

func parseEncryptionKey(raw string) ([]byte, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("ENCRYPTION_KEY is required")
	}
	if decoded, err := base64.StdEncoding.DecodeString(raw); err == nil && len(decoded) == 32 {
		return decoded, nil
	}
	if decoded, err := hex.DecodeString(raw); err == nil && len(decoded) == 32 {
		return decoded, nil
	}
	return nil, fmt.Errorf("ENCRYPTION_KEY must be exactly 32 bytes encoded as base64 or 64 hexadecimal characters")
}
