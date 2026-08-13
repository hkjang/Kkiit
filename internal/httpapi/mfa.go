package httpapi

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" // #nosec G505 -- RFC 6238 compatibility requires HMAC-SHA1.
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const totpPeriod = 30 * time.Second

func (s *Server) mfaStatus(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	var name string
	var confirmedAt *time.Time
	err := s.DB.QueryRow(r.Context(), `SELECT name,confirmed_at FROM mfa_factors WHERE user_id=$1 AND factor_type='totp' AND enabled ORDER BY confirmed_at DESC LIMIT 1`, p.UserID).Scan(&name, &confirmedAt)
	if err == pgx.ErrNoRows {
		writeJSON(w, 200, map[string]any{"totp_enabled": false})
		return
	}
	if err != nil {
		writeError(w, 500, "query_failed", "MFA 상태를 조회하지 못했습니다.")
		return
	}
	writeJSON(w, 200, map[string]any{"totp_enabled": true, "name": name, "confirmed_at": confirmedAt})
}

func (s *Server) setupTOTP(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	var exists bool
	if err := s.DB.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM mfa_factors WHERE user_id=$1 AND factor_type='totp' AND enabled)`, p.UserID).Scan(&exists); err != nil {
		writeError(w, 500, "query_failed", "MFA 상태를 확인하지 못했습니다.")
		return
	}
	if exists {
		writeError(w, 409, "mfa_already_enabled", "이미 TOTP 인증이 활성화되어 있습니다.")
		return
	}
	secretBytes := make([]byte, 20)
	if _, err := rand.Read(secretBytes); err != nil {
		writeError(w, 500, "random_failed", "MFA 비밀값을 만들지 못했습니다.")
		return
	}
	secret := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(secretBytes)
	factorID := uuid.New()
	encrypted, err := s.Box.Encrypt([]byte(secret), "mfa:"+factorID.String())
	if err != nil {
		writeError(w, 500, "encryption_failed", "MFA 비밀값을 암호화하지 못했습니다.")
		return
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		writeError(w, 500, "transaction_failed", "MFA 등록을 시작하지 못했습니다.")
		return
	}
	defer tx.Rollback(r.Context()) //nolint:errcheck
	if _, err = tx.Exec(r.Context(), `DELETE FROM mfa_factors WHERE user_id=$1 AND factor_type='totp' AND NOT enabled`, p.UserID); err == nil {
		_, err = tx.Exec(r.Context(), `INSERT INTO mfa_factors(id,user_id,factor_type,name,secret_encrypted) VALUES($1,$2,'totp','Authenticator',$3)`, factorID, p.UserID, encrypted)
	}
	if err != nil || tx.Commit(r.Context()) != nil {
		writeError(w, 500, "mfa_setup_failed", "MFA 등록 정보를 저장하지 못했습니다.")
		return
	}
	issuer := "Kkiit"
	account := p.Username
	otpauth := "otpauth://totp/" + url.PathEscape(issuer+":"+account) + "?" + url.Values{"secret": {secret}, "issuer": {issuer}, "algorithm": {"SHA1"}, "digits": {"6"}, "period": {"30"}}.Encode()
	s.audit(r, "mfa.setup", "mfa_factor", factorID.String(), nil, map[string]any{"factor_type": "totp"}, "success")
	writeJSON(w, 201, map[string]any{"secret": secret, "otpauth_uri": otpauth, "warning": "확인 코드 입력 전에는 활성화되지 않습니다."})
}

func (s *Server) confirmTOTP(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	code, ok := decodeTOTPCode(w, r)
	if !ok {
		return
	}
	var id uuid.UUID
	var encrypted []byte
	err := s.DB.QueryRow(r.Context(), `SELECT id,secret_encrypted FROM mfa_factors WHERE user_id=$1 AND factor_type='totp' AND NOT enabled ORDER BY created_at DESC LIMIT 1`, p.UserID).Scan(&id, &encrypted)
	if err != nil {
		writeError(w, 404, "mfa_setup_not_found", "확인할 MFA 등록 요청이 없습니다.")
		return
	}
	secret, err := s.Box.Decrypt(encrypted, "mfa:"+id.String())
	if err != nil || !verifyTOTP(string(secret), code, time.Now()) {
		writeError(w, 400, "invalid_mfa_code", "인증 앱의 6자리 코드를 확인해 주세요.")
		return
	}
	if _, err := s.DB.Exec(r.Context(), `UPDATE mfa_factors SET enabled=true,confirmed_at=now(),last_used_at=now() WHERE id=$1`, id); err != nil {
		writeError(w, 500, "mfa_confirm_failed", "MFA를 활성화하지 못했습니다.")
		return
	}
	s.audit(r, "mfa.enable", "mfa_factor", id.String(), nil, map[string]any{"enabled": true}, "success")
	writeJSON(w, 200, map[string]any{"totp_enabled": true})
}

func (s *Server) disableTOTP(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	code, ok := decodeTOTPCode(w, r)
	if !ok {
		return
	}
	var id uuid.UUID
	var encrypted []byte
	err := s.DB.QueryRow(r.Context(), `SELECT id,secret_encrypted FROM mfa_factors WHERE user_id=$1 AND factor_type='totp' AND enabled ORDER BY confirmed_at DESC LIMIT 1`, p.UserID).Scan(&id, &encrypted)
	if err != nil {
		writeError(w, 404, "mfa_not_enabled", "활성화된 TOTP 인증이 없습니다.")
		return
	}
	secret, err := s.Box.Decrypt(encrypted, "mfa:"+id.String())
	if err != nil || !verifyTOTP(string(secret), code, time.Now()) {
		writeError(w, 400, "invalid_mfa_code", "인증 앱의 6자리 코드를 확인해 주세요.")
		return
	}
	if _, err := s.DB.Exec(r.Context(), `DELETE FROM mfa_factors WHERE id=$1`, id); err != nil {
		writeError(w, 500, "mfa_disable_failed", "MFA를 해제하지 못했습니다.")
		return
	}
	s.audit(r, "mfa.disable", "mfa_factor", id.String(), map[string]any{"enabled": true}, map[string]any{"enabled": false}, "success")
	w.WriteHeader(http.StatusNoContent)
}

func decodeTOTPCode(w http.ResponseWriter, r *http.Request) (string, bool) {
	var input struct {
		Code string `json:"code"`
	}
	if !decodeJSON(w, r, &input) {
		return "", false
	}
	input.Code = strings.TrimSpace(input.Code)
	if len(input.Code) != 6 {
		writeError(w, 400, "invalid_mfa_code", "6자리 인증 코드를 입력해 주세요.")
		return "", false
	}
	return input.Code, true
}

func (s *Server) verifyLoginTOTP(ctx context.Context, userID uuid.UUID, code string) (required bool, valid bool, err error) {
	var id uuid.UUID
	var encrypted []byte
	err = s.DB.QueryRow(ctx, `SELECT id,secret_encrypted FROM mfa_factors WHERE user_id=$1 AND factor_type='totp' AND enabled ORDER BY confirmed_at DESC LIMIT 1`, userID).Scan(&id, &encrypted)
	if err == pgx.ErrNoRows {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	secret, err := s.Box.Decrypt(encrypted, "mfa:"+id.String())
	if err != nil {
		return true, false, err
	}
	if !verifyTOTP(string(secret), strings.TrimSpace(code), time.Now()) {
		return true, false, nil
	}
	_, _ = s.DB.Exec(ctx, `UPDATE mfa_factors SET last_used_at=now() WHERE id=$1`, id)
	return true, true, nil
}

func verifyTOTP(secret, code string, at time.Time) bool {
	decoded, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(strings.TrimSpace(secret)))
	if err != nil || len(code) != 6 {
		return false
	}
	counter := at.Unix() / int64(totpPeriod/time.Second)
	for drift := int64(-1); drift <= 1; drift++ {
		if subtle.ConstantTimeCompare([]byte(totpCode(decoded, uint64(counter+drift))), []byte(code)) == 1 {
			return true
		}
	}
	return false
}

func totpCode(secret []byte, counter uint64) string {
	message := make([]byte, 8)
	binary.BigEndian.PutUint64(message, counter)
	mac := hmac.New(sha1.New, secret) // #nosec G401 -- RFC 6238 compatibility requires HMAC-SHA1.
	_, _ = mac.Write(message)
	digest := mac.Sum(nil)
	offset := digest[len(digest)-1] & 0x0f
	value := binary.BigEndian.Uint32(digest[offset:offset+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", value%1_000_000)
}
