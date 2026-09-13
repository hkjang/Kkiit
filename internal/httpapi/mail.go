package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/hkjang/Kkiit/internal/mail"
)

// validateMailSetting refuses a mail setting that could never send. The relay
// details are checked whenever they are present; the parts that only matter
// once mail is on are demanded only then, so an operator can fill the form in
// two sittings.
func validateMailSetting(value map[string]any) error {
	config := mail.ReadConfig(value, "")
	if !config.Enabled {
		if config.Host == "" {
			return nil
		}
		// A host that is typed in should be a host the relay can be reached at.
		config.FromAddress = "check@" + config.Host
	}
	if err := config.Validate(); err != nil {
		return errors.New(strings.TrimPrefix(err.Error(), mail.ErrInvalid.Error()+": "))
	}
	return nil
}

// listMailDeliveries shows what left the building: every attempt with its
// outcome, newest first, and a count per status. Bodies are never returned.
func (s *Server) listMailDeliveries(w http.ResponseWriter, r *http.Request) {
	args := []any{queryLimit(r, 100, 500)}
	filter := ""
	if status := strings.TrimSpace(r.URL.Query().Get("status")); status != "" {
		filter = ` WHERE status=$2`
		args = append(args, status)
	}
	rows, err := s.DB.Query(r.Context(), `SELECT id,event_id,event_type,user_id,recipient,subject,link,status,attempts,COALESCE(last_error,''),next_attempt_at,sent_at,created_at,updated_at
		FROM mail_deliveries`+filter+` ORDER BY created_at DESC,id LIMIT $1`, args...)
	if err != nil {
		writeError(w, 500, "query_failed", "발송 기록을 조회하지 못했습니다.")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id uuid.UUID
		var eventID, userID *uuid.UUID
		var eventType, recipient, subject, link, state, lastError string
		var attempts int
		var nextAttempt, created, updated time.Time
		var sentAt *time.Time
		if rows.Scan(&id, &eventID, &eventType, &userID, &recipient, &subject, &link, &state, &attempts, &lastError, &nextAttempt, &sentAt, &created, &updated) != nil {
			continue
		}
		items = append(items, map[string]any{"id": id, "event_id": eventID, "event_type": eventType, "user_id": userID, "recipient": recipient, "subject": subject, "link": link,
			"status": state, "attempts": attempts, "last_error": lastError, "next_attempt_at": nextAttempt, "sent_at": sentAt, "created_at": created, "updated_at": updated})
	}
	summary := map[string]int{}
	total := 0
	counts, err := s.DB.Query(r.Context(), `SELECT status,count(*) FROM mail_deliveries GROUP BY 1`)
	if err == nil {
		defer counts.Close()
		for counts.Next() {
			var key string
			var count int
			if counts.Scan(&key, &count) == nil {
				summary[key] = count
				total += count
			}
		}
	}
	writeJSON(w, 200, map[string]any{"items": items, "summary": map[string]any{"total": total, "status": summary}})
}

// sendTestMail sends one real message with the saved setting and reports the
// outcome in the response. Relay settings are rarely right the first time,
// and the reason has to be readable where the form is.
func (s *Server) sendTestMail(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	var in struct {
		Recipient string `json:"recipient"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	recipient := strings.TrimSpace(in.Recipient)
	if recipient == "" {
		_ = s.DB.QueryRow(r.Context(), `SELECT COALESCE(email,'') FROM users WHERE id=$1`, p.UserID).Scan(&recipient)
	}
	if !strings.Contains(recipient, "@") {
		writeError(w, 400, "invalid_recipient", "받는 사람 메일 주소를 입력해 주세요.")
		return
	}
	config, err := mail.Load(r.Context(), s.DB, s.Box)
	if err != nil {
		writeError(w, 500, "mail_config_failed", err.Error())
		return
	}
	if !config.Enabled {
		writeError(w, 409, "mail_disabled", "mail.enabled 가 꺼져 있습니다. 먼저 켜고 저장한 뒤 시험 발송하세요.")
		return
	}
	if config.Timeout > 30*time.Second {
		config.Timeout = 30 * time.Second
	}
	id := uuid.New()
	message := mail.TestMessage(config, recipient)
	started := time.Now()
	sendErr := mail.Deliver(config, message)
	// The attempt is recorded either way, so the log answers "did the test go
	// out" the same way it answers it for every other mail.
	recordCtx := context.WithoutCancel(r.Context())
	status, lastError := "sent", ""
	var sentAt *time.Time
	if sendErr != nil {
		status, lastError = "failed", sendErr.Error()
	} else {
		now := time.Now()
		sentAt = &now
	}
	if _, err := s.DB.Exec(recordCtx, `INSERT INTO mail_deliveries(id,event_type,user_id,recipient,subject,status,attempts,last_error,sent_at) VALUES($1,$2,$3,$4,$5,$6,1,NULLIF($7,''),$8)`,
		id, mail.EventTest, p.UserID, recipient, message.Subject, status, lastError, sentAt); err != nil {
		s.Logger.Warn("test mail was not recorded", "error", err)
	}
	s.audit(r, "mail.test", "mail_delivery", id.String(), nil, map[string]any{"recipient": recipient, "relay": config.Host, "status": status}, status)
	if sendErr != nil {
		writeJSON(w, 502, map[string]any{"error": map[string]any{"code": "mail_send_failed", "message": sendErr.Error()}, "delivery_id": id, "elapsed_ms": time.Since(started).Milliseconds()})
		return
	}
	writeJSON(w, 200, map[string]any{"sent": true, "recipient": recipient, "delivery_id": id, "elapsed_ms": time.Since(started).Milliseconds()})
}
