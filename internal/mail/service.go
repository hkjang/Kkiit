package mail

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/hkjang/Kkiit/internal/cryptox"
)

// Querier is the one database method Load needs, satisfied by a pool and a
// transaction alike.
type Querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Load reads the setting row and decrypts the password with the same purpose
// string the settings API encrypted it under. The password exists only in the
// returned value; it is never logged and never written back.
//
// The link base falls back to the external address the login flow already
// knows, so a mail carries a working link on an installation that never set
// mail.base_url.
func Load(ctx context.Context, db Querier, box *cryptox.Box) (Config, error) {
	var raw, encrypted []byte
	var fallbackBase string
	err := db.QueryRow(ctx, `SELECT value,encrypted_value,COALESCE((SELECT value->>'callback_base_url' FROM system_settings WHERE key='auth.oauth'),'') FROM system_settings WHERE key=$1`, SettingKey).Scan(&raw, &encrypted, &fallbackBase)
	if err == pgx.ErrNoRows {
		return ReadConfig(nil, ""), nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("load mail setting: %w", err)
	}
	password := ""
	if len(encrypted) > 0 && box != nil {
		plain, err := box.Decrypt(encrypted, "setting:"+SettingKey)
		if err != nil {
			return Config{}, fmt.Errorf("%w: 저장된 SMTP 비밀번호를 복호화하지 못했습니다", ErrInvalid)
		}
		password = string(plain)
	}
	var values map[string]any
	_ = json.Unmarshal(raw, &values)
	config := ReadConfig(values, password)
	if strings.TrimSpace(config.BaseURL) == "" {
		config.BaseURL = strings.TrimSpace(fallbackBase)
	}
	return config, nil
}
