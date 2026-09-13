package worker

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/hkjang/Kkiit/internal/mail"
)

// mailCoalesceDelay is how long a queued mail waits before it is sent. Events
// from one action arrive a moment apart; waiting lets them fold into one
// message per person instead of three.
const mailCoalesceDelay = 10 * time.Second

// Relays dislike many parallel connections from one host more than they like
// throughput, so two is plenty.
const mailConcurrency = 2

// mailMaxAttempts covers a relay that is briefly refusing connections. Beyond
// this the row is failed where the console can see it.
const mailMaxAttempts = 4

type queuedMail struct {
	ID        uuid.UUID
	Recipient string
	Subject   string
	Link      string
	Body      string
	Attempts  int
}

// queueMail records one delivery per recipient inside the event transaction.
// Nothing is sent here: the row is what makes the mail survive a restart and
// what the console shows afterwards.
func (w *Worker) queueMail(ctx context.Context, tx pgx.Tx, current policy, event outboxEvent, target subject) error {
	if !current.Mail.Enabled || !current.Mail.Allows(event.EventType) {
		return nil
	}
	spec, _ := mail.Lookup(event.EventType)
	var candidates []uuid.UUID
	switch spec.Audience {
	case mail.AudienceBuyer:
		candidates = []uuid.UUID{target.Buyer}
	case mail.AudienceSeller:
		candidates = []uuid.UUID{target.Seller}
	default:
		candidates = []uuid.UUID{target.Buyer, target.Seller}
	}
	actor, hasActor := parseUUIDValue(event.Payload["actor"])
	recipients := make([]uuid.UUID, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate == uuid.Nil || (hasActor && candidate == actor) {
			continue
		}
		duplicate := false
		for _, existing := range recipients {
			duplicate = duplicate || existing == candidate
		}
		if !duplicate {
			recipients = append(recipients, candidate)
		}
	}
	if len(recipients) == 0 {
		return nil
	}
	// The inbox templates are the wording people already know, and an operator
	// who turned a template off meant it for every channel.
	var subjectTemplate, bodyTemplate string
	var enabled bool
	err := tx.QueryRow(ctx, `SELECT subject_template,body_template,enabled FROM notification_templates WHERE key=$1 AND channel='web'`, event.EventType).Scan(&subjectTemplate, &bodyTemplate, &enabled)
	if err == pgx.ErrNoRows || (err == nil && !enabled) {
		return nil
	}
	if err != nil {
		return err
	}
	subjectText, bodyText := render(subjectTemplate, target.Variables), render(bodyTemplate, target.Variables)
	for _, recipient := range recipients {
		// The person decides. A switched off event stays off for mail too.
		var wants bool
		err := tx.QueryRow(ctx, `SELECT enabled FROM notification_preferences WHERE user_id=$1 AND event_type=$2`, recipient, event.EventType).Scan(&wants)
		if err == nil && !wants {
			continue
		}
		if err != nil && err != pgx.ErrNoRows {
			return err
		}
		// The user table is the directory. An account without an address, or
		// one that is no longer active, simply gets nothing.
		var address string
		err = tx.QueryRow(ctx, `SELECT email FROM users WHERE id=$1 AND status='active' AND email IS NOT NULL AND email<>''`, recipient).Scan(&address)
		if err == pgx.ErrNoRows {
			continue
		}
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO mail_deliveries(id,event_id,event_type,user_id,recipient,subject,link,body,status,next_attempt_at)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,'queued',now()+make_interval(secs => $9)) ON CONFLICT DO NOTHING`,
			uuid.New(), event.ID, event.EventType, recipient, strings.TrimSpace(address), truncate(subjectText, 300), target.Link, bodyText, int(mailCoalesceDelay/time.Second)); err != nil {
			return err
		}
	}
	return nil
}

// deliverMail sends what is due, one message per recipient. It runs on the
// dispatcher's tick, never on a request.
func (w *Worker) deliverMail(ctx context.Context, current policy) int {
	if !current.Mail.Enabled {
		w.failQueuedMailWhileOff(ctx)
		return 0
	}
	// Once one row for a person is due, everything still queued for them goes
	// in the same message; only rows waiting out a retry backoff stay behind.
	rows, err := w.DB.Query(ctx, `UPDATE mail_deliveries SET status='sending',attempts=attempts+1,updated_at=now()
		WHERE id IN (SELECT m.id FROM mail_deliveries m
			WHERE m.status IN ('queued','retry') AND (m.status='queued' OR m.next_attempt_at<=now())
				AND lower(m.recipient) IN (SELECT lower(recipient) FROM mail_deliveries WHERE status IN ('queued','retry') AND next_attempt_at<=now() ORDER BY next_attempt_at LIMIT $1)
			FOR UPDATE SKIP LOCKED)
		RETURNING id,recipient,subject,link,COALESCE(body,''),attempts`, current.DeliveryBatch)
	if err != nil {
		if ctx.Err() == nil {
			w.Logger.Error("claim mail failed", "error", err)
		}
		return 0
	}
	byRecipient := map[string][]queuedMail{}
	order := make([]string, 0)
	total := 0
	for rows.Next() {
		var item queuedMail
		if rows.Scan(&item.ID, &item.Recipient, &item.Subject, &item.Link, &item.Body, &item.Attempts) != nil {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(item.Recipient))
		if _, seen := byRecipient[key]; !seen {
			order = append(order, key)
		}
		byRecipient[key] = append(byRecipient[key], item)
		total++
	}
	rows.Close()
	if total == 0 {
		return 0
	}
	// A configuration that cannot send fails every row with the reason instead
	// of dialing nothing forever. The console shows the reason on each row.
	if err := current.Mail.Validate(); err != nil {
		w.Logger.Warn("mail is enabled but cannot send", "error", err)
		for _, items := range byRecipient {
			w.recordMail(ctx, items, err, true)
		}
		return total
	}
	queue := make(chan []queuedMail)
	var group sync.WaitGroup
	for i := 0; i < mailConcurrency; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			for items := range queue {
				w.sendMail(ctx, current.Mail, items)
			}
		}()
	}
	for _, key := range order {
		queue <- byRecipient[key]
	}
	close(queue)
	group.Wait()
	return total
}

func (w *Worker) sendMail(ctx context.Context, config mail.Config, items []queuedMail) {
	parts := make([]mail.Item, 0, len(items))
	for _, item := range items {
		parts = append(parts, mail.Item{Subject: item.Subject, Body: item.Body, Link: item.Link})
	}
	err := mail.Deliver(config, mail.Digest(config, items[0].Recipient, parts))
	w.recordMail(ctx, items, err, false)
}

// recordMail writes the outcome of one attempt for every row that went into
// the message. The body is dropped as soon as the row is final.
func (w *Worker) recordMail(ctx context.Context, items []queuedMail, cause error, final bool) {
	// The result must be recorded even while shutting down, otherwise the row
	// stays in sending until the stuck sweeper reclaims it.
	recordCtx := context.WithoutCancel(ctx)
	if cause == nil {
		for _, item := range items {
			if _, err := w.DB.Exec(recordCtx, `UPDATE mail_deliveries SET status='sent',body=NULL,last_error=NULL,sent_at=now(),updated_at=now() WHERE id=$1`, item.ID); err != nil {
				w.Logger.Error("record mail success failed", "error", err, "delivery_id", item.ID)
			}
		}
		return
	}
	message := truncate(cause.Error(), 500)
	for _, item := range items {
		status := "retry"
		if final || item.Attempts >= mailMaxAttempts {
			status = "failed"
		}
		w.Logger.Warn("notification mail failed", "error", message, "delivery_id", item.ID, "recipient", item.Recipient, "attempts", item.Attempts, "status", status)
		if _, err := w.DB.Exec(recordCtx, `UPDATE mail_deliveries SET status=$2,last_error=$3,body=CASE WHEN $2='failed' THEN NULL ELSE body END,next_attempt_at=now()+make_interval(secs => $4),updated_at=now() WHERE id=$1`,
			item.ID, status, message, int(backoff(item.Attempts, 30*time.Second, 30*time.Minute)/time.Second)); err != nil {
			w.Logger.Error("record mail failure failed", "error", err, "delivery_id", item.ID)
		}
	}
}

// failQueuedMailWhileOff closes rows that were queued before mail was turned
// off. Sending them hours later, when somebody turns it back on, would be a
// surprise; a row that says why it was not sent is not. The sweep is rare
// because a fresh installation, where mail is off, should not pay for it.
func (w *Worker) failQueuedMailWhileOff(ctx context.Context) {
	if time.Since(w.lastMailSweep) < time.Minute {
		return
	}
	w.lastMailSweep = time.Now()
	if _, err := w.DB.Exec(ctx, `UPDATE mail_deliveries SET status='failed',body=NULL,last_error='mail.enabled 가 꺼져 있어 보내지 않았습니다',updated_at=now() WHERE status IN ('queued','retry')`); err != nil && ctx.Err() == nil {
		w.Logger.Error("close queued mail failed", "error", err)
	}
}

func truncate(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}
