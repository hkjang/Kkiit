// Package worker drains the transactional outbox. Business transactions only
// append to domain_events; turning an event into an inbox notification or a
// signed webhook call happens here, out of the request path, with retries and a
// visible failure state.
package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hkjang/Kkiit/internal/cryptox"
	"github.com/hkjang/Kkiit/internal/mail"
)

type Worker struct {
	DB      *pgxpool.Pool
	Box     *cryptox.Box
	Logger  *slog.Logger
	Publish func(userID uuid.UUID, event any)
	// Maintenance advances orders that only the passage of time should move.
	// It lives in the HTTP layer so the state machine and settlement logic are
	// written once; nil simply skips the sweep.
	Maintenance func(ctx context.Context) (expired int, accepted int)

	policyMu        sync.Mutex
	policy          policy
	policyLoaded    time.Time
	lastCleanupAt   time.Time
	lastRiskScan    time.Time
	lastTrustScan   time.Time
	lastMaintenance time.Time
	lastMailSweep   time.Time
}

type policy struct {
	PollInterval    time.Duration
	EventBatch      int
	DeliveryBatch   int
	EventRetryLimit int
	StuckAfter      time.Duration
	Retention       time.Duration
	WebhookEnabled  bool
	MaxAttempts     int
	Timeout         time.Duration
	AllowPrivate    bool
	WebChannel      bool
	RiskEnabled     bool
	RiskBatch       int
	RiskInterval    time.Duration
	Risk            riskThresholds
	SettlementAuto  bool
	SettlementBatch int
	TrustEnabled    bool
	TrustBatch      int
	TrustInterval   time.Duration
	SellerLevels    []sellerLevel
	Mail            mail.Config
}

func defaultPolicy() policy {
	return policy{PollInterval: 2 * time.Second, EventBatch: 50, DeliveryBatch: 25, EventRetryLimit: 10,
		StuckAfter: 5 * time.Minute, Retention: 90 * 24 * time.Hour, WebhookEnabled: true, MaxAttempts: 6,
		Timeout: 10 * time.Second, AllowPrivate: true, WebChannel: true,
		RiskEnabled: true, RiskBatch: 200, RiskInterval: 5 * time.Minute, Risk: defaultRiskThresholds(),
		SettlementAuto: false, SettlementBatch: 100,
		TrustEnabled: true, TrustBatch: 500, TrustInterval: 15 * time.Minute, SellerLevels: defaultSellerLevels(),
		Mail: mail.ReadConfig(nil, "")}
}

type outboxEvent struct {
	ID            uuid.UUID
	AggregateType string
	AggregateID   uuid.UUID
	EventType     string
	Payload       map[string]any
	Attempts      int
	CreatedAt     time.Time
}

// Run drains the outbox until the context is cancelled. One instance per
// process is enough; several instances stay correct because every claim uses
// FOR UPDATE SKIP LOCKED.
func (w *Worker) Run(ctx context.Context) {
	w.Logger.Info("event dispatcher started")
	for {
		current := w.currentPolicy(ctx)
		worked := w.tick(ctx, current)
		if ctx.Err() != nil {
			w.Logger.Info("event dispatcher stopped")
			return
		}
		if worked {
			continue
		}
		select {
		case <-ctx.Done():
			w.Logger.Info("event dispatcher stopped")
			return
		case <-time.After(current.PollInterval):
		}
	}
}

func (w *Worker) tick(ctx context.Context, current policy) bool {
	w.reclaimStuck(ctx, current)
	w.cleanup(ctx, current)
	w.periodicScans(ctx, current)
	events := w.dispatchEvents(ctx, current)
	deliveries := w.deliverWebhooks(ctx, current)
	mails := w.deliverMail(ctx, current)
	return events > 0 || deliveries > 0 || mails > 0
}

// ScanNow runs the periodic analyses immediately and reports what changed. The
// operator console uses it so a rule or threshold change can be seen without
// waiting for the next interval.
func (w *Worker) ScanNow(ctx context.Context) (int, int) {
	current := w.currentPolicy(ctx)
	w.lastRiskScan, w.lastTrustScan = time.Now(), time.Now()
	risk := w.scanRisk(ctx, current)
	settlements := w.processDueSettlements(ctx, current)
	w.scoreSellers(ctx, current)
	w.scoreTalents(ctx, current)
	if w.Maintenance != nil {
		w.lastMaintenance = time.Now()
		w.Maintenance(ctx)
	}
	return risk, settlements
}

// periodicScans runs the analyses that look at the whole table rather than at a
// queue, so they follow their own interval instead of the outbox poll.
func (w *Worker) periodicScans(ctx context.Context, current policy) {
	if time.Since(w.lastRiskScan) >= current.RiskInterval {
		w.lastRiskScan = time.Now()
		w.scanRisk(ctx, current)
		w.processDueSettlements(ctx, current)
	}
	if w.Maintenance != nil && time.Since(w.lastMaintenance) >= current.RiskInterval {
		w.lastMaintenance = time.Now()
		if expired, accepted := w.Maintenance(ctx); expired > 0 || accepted > 0 {
			w.Logger.Info("order maintenance", "expired", expired, "auto_accepted", accepted)
		}
		w.flagOverdueOrders(ctx)
	}
	if time.Since(w.lastTrustScan) >= current.TrustInterval {
		w.lastTrustScan = time.Now()
		// Talent quality reads the seller score, so sellers are graded first.
		sellers := w.scoreSellers(ctx, current)
		talents := w.scoreTalents(ctx, current)
		if sellers > 0 || talents > 0 {
			w.Logger.Info("trust scan updated scores", "sellers", sellers, "talents", talents)
		}
	}
}

// ReloadPolicy drops the cached settings so the next tick reads them again.
// Tests use it; in production a change reaches the dispatcher within the
// thirty second cache window.
func (w *Worker) ReloadPolicy() {
	w.policyMu.Lock()
	defer w.policyMu.Unlock()
	w.policyLoaded = time.Time{}
}

func (w *Worker) currentPolicy(ctx context.Context) policy {
	w.policyMu.Lock()
	defer w.policyMu.Unlock()
	if time.Since(w.policyLoaded) < 30*time.Second && !w.policyLoaded.IsZero() {
		return w.policy
	}
	result := defaultPolicy()
	var dispatchRaw, webhookRaw, channelRaw []byte
	err := w.DB.QueryRow(ctx, `SELECT
		COALESCE((SELECT value FROM system_settings WHERE key='notification.dispatch'),'{}'::jsonb),
		COALESCE((SELECT value FROM system_settings WHERE key='notification.webhook'),'{}'::jsonb),
		COALESCE((SELECT value FROM system_settings WHERE key='notification.channels'),'{}'::jsonb)`).Scan(&dispatchRaw, &webhookRaw, &channelRaw)
	if err == nil {
		dispatch, webhook, channels := decodeObject(dispatchRaw), decodeObject(webhookRaw), decodeObject(channelRaw)
		result.PollInterval = durationSetting(dispatch, "poll_seconds", time.Second, result.PollInterval, time.Second, time.Minute)
		result.EventBatch = intSetting(dispatch, "event_batch", result.EventBatch, 1, 500)
		result.DeliveryBatch = intSetting(dispatch, "delivery_batch", result.DeliveryBatch, 1, 200)
		result.EventRetryLimit = intSetting(dispatch, "event_retry_limit", result.EventRetryLimit, 1, 50)
		result.StuckAfter = durationSetting(dispatch, "stuck_after_minutes", time.Minute, result.StuckAfter, time.Minute, 24*time.Hour)
		result.Retention = durationSetting(dispatch, "retention_days", 24*time.Hour, result.Retention, 24*time.Hour, 3650*24*time.Hour)
		result.WebhookEnabled = boolSetting(webhook, "enabled", result.WebhookEnabled)
		result.MaxAttempts = intSetting(webhook, "max_attempts", result.MaxAttempts, 1, 20)
		result.Timeout = durationSetting(webhook, "timeout_seconds", time.Second, result.Timeout, time.Second, 2*time.Minute)
		result.AllowPrivate = boolSetting(webhook, "allow_private_targets", result.AllowPrivate)
		result.WebChannel = boolSetting(channels, "web", result.WebChannel)
	}
	var riskRaw, settlementRaw []byte
	if err := w.DB.QueryRow(ctx, `SELECT
		COALESCE((SELECT value FROM system_settings WHERE key='risk.policy'),'{}'::jsonb),
		COALESCE((SELECT value FROM system_settings WHERE key='settlement.policy'),'{}'::jsonb)`).Scan(&riskRaw, &settlementRaw); err == nil {
		risk, settlement := decodeObject(riskRaw), decodeObject(settlementRaw)
		result.RiskEnabled = boolSetting(risk, "enabled", result.RiskEnabled)
		result.RiskBatch = intSetting(risk, "scan_batch", result.RiskBatch, 1, 2000)
		result.RiskInterval = durationSetting(risk, "scan_interval_minutes", time.Minute, result.RiskInterval, time.Minute, 24*time.Hour)
		result.Risk.High = intSetting(risk, "high_threshold", result.Risk.High, 1, 100)
		result.Risk.Critical = intSetting(risk, "critical_threshold", result.Risk.Critical, 1, 100)
		result.Risk.Medium = intSetting(risk, "medium_threshold", result.Risk.Medium, 1, 100)
		result.Risk.AutoHoldSettlemnt = boolSetting(risk, "auto_hold_settlement", result.Risk.AutoHoldSettlemnt)
		result.SettlementAuto = boolSetting(settlement, "batch_enabled", result.SettlementAuto)
		result.SettlementBatch = intSetting(settlement, "batch_size", result.SettlementBatch, 1, 1000)
		if levels := stringsSetting(settlement, "hold_levels"); levels != nil {
			result.Risk.HoldLevels = levels
		}
	}
	var gradingRaw []byte
	if err := w.DB.QueryRow(ctx, `SELECT COALESCE((SELECT value FROM system_settings WHERE key='seller.grading'),'{}'::jsonb)`).Scan(&gradingRaw); err == nil {
		grading := decodeObject(gradingRaw)
		result.TrustEnabled = boolSetting(grading, "enabled", result.TrustEnabled)
		result.TrustBatch = intSetting(grading, "scan_batch", result.TrustBatch, 1, 5000)
		result.TrustInterval = durationSetting(grading, "scan_interval_minutes", time.Minute, result.TrustInterval, time.Minute, 24*time.Hour)
		if levels := sellerLevelsSetting(grading); len(levels) > 0 {
			result.SellerLevels = levels
		}
	}
	// The relay password is decrypted here and lives only in this value. A
	// row that cannot be read leaves mail off rather than half configured.
	if config, err := mail.Load(ctx, w.DB, w.Box); err == nil {
		result.Mail = config
	} else {
		w.Logger.Warn("mail setting was not read", "error", err)
	}
	w.policy, w.policyLoaded = result, time.Now()
	return result
}

func decodeObject(raw []byte) map[string]any {
	var value map[string]any
	if json.Unmarshal(raw, &value) != nil {
		return map[string]any{}
	}
	return value
}

func intSetting(source map[string]any, key string, fallback, min, max int) int {
	number, ok := source[key].(float64)
	if !ok {
		return fallback
	}
	value := int(number)
	if value < min || value > max {
		return fallback
	}
	return value
}

func sellerLevelsSetting(source map[string]any) []sellerLevel {
	raw, ok := source["levels"].([]any)
	if !ok {
		return nil
	}
	levels := make([]sellerLevel, 0, len(raw))
	for _, item := range raw {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		code, _ := entry["code"].(string)
		if code == "" {
			continue
		}
		score, _ := entry["min_score"].(float64)
		orders, _ := entry["min_orders"].(float64)
		levels = append(levels, sellerLevel{Code: code, MinScore: int(score), MinOrders: int(orders)})
	}
	return levels
}

func stringsSetting(source map[string]any, key string) []string {
	raw, ok := source[key].([]any)
	if !ok {
		return nil
	}
	values := make([]string, 0, len(raw))
	for _, item := range raw {
		if text, ok := item.(string); ok {
			values = append(values, text)
		}
	}
	return values
}

func boolSetting(source map[string]any, key string, fallback bool) bool {
	if value, ok := source[key].(bool); ok {
		return value
	}
	return fallback
}

func durationSetting(source map[string]any, key string, unit, fallback, min, max time.Duration) time.Duration {
	number, ok := source[key].(float64)
	if !ok {
		return fallback
	}
	value := time.Duration(number) * unit
	if value < min || value > max {
		return fallback
	}
	return value
}

// reclaimStuck returns events left in processing by a crashed process so a
// restart never strands a business event.
func (w *Worker) reclaimStuck(ctx context.Context, current policy) {
	minutes := int(current.StuckAfter / time.Minute)
	if _, err := w.DB.Exec(ctx, `UPDATE domain_events SET status='retry',available_at=now() WHERE status='processing' AND created_at < now()-make_interval(mins => $1)`, minutes); err != nil && ctx.Err() == nil {
		w.Logger.Error("reclaim stuck events failed", "error", err)
	}
	if _, err := w.DB.Exec(ctx, `UPDATE webhook_deliveries SET state='retry',next_attempt_at=now() WHERE state='sending' AND created_at < now()-make_interval(mins => $1)`, minutes); err != nil && ctx.Err() == nil {
		w.Logger.Error("reclaim stuck deliveries failed", "error", err)
	}
	if _, err := w.DB.Exec(ctx, `UPDATE mail_deliveries SET status='retry',next_attempt_at=now() WHERE status='sending' AND updated_at < now()-make_interval(mins => $1)`, minutes); err != nil && ctx.Err() == nil {
		w.Logger.Error("reclaim stuck mail failed", "error", err)
	}
}

func (w *Worker) cleanup(ctx context.Context, current policy) {
	if time.Since(w.lastCleanupAt) < time.Hour {
		return
	}
	w.lastCleanupAt = time.Now()
	days := int(current.Retention / (24 * time.Hour))
	if _, err := w.DB.Exec(ctx, `DELETE FROM domain_events WHERE status='done' AND processed_at < now()-make_interval(days => $1)`, days); err != nil && ctx.Err() == nil {
		w.Logger.Error("event retention cleanup failed", "error", err)
	}
	if _, err := w.DB.Exec(ctx, `DELETE FROM mail_deliveries WHERE status IN ('sent','failed') AND created_at < now()-make_interval(days => $1)`, days); err != nil && ctx.Err() == nil {
		w.Logger.Error("mail retention cleanup failed", "error", err)
	}
	// Credentials that can no longer authenticate anything are dead weight, and
	// keeping them around only widens what a database copy exposes.
	for _, statement := range []string{
		`DELETE FROM oauth_states WHERE expires_at < now()-interval '1 day'`,
		`DELETE FROM login_challenges WHERE expires_at < now()-interval '1 day'`,
		`DELETE FROM sessions WHERE expires_at < now()-interval '30 days' OR (revoked_at IS NOT NULL AND revoked_at < now()-interval '30 days')`,
	} {
		if _, err := w.DB.Exec(ctx, statement); err != nil && ctx.Err() == nil {
			w.Logger.Error("credential cleanup failed", "error", err)
		}
	}
}

func (w *Worker) dispatchEvents(ctx context.Context, current policy) int {
	rows, err := w.DB.Query(ctx, `UPDATE domain_events SET status='processing',attempts=attempts+1
		WHERE id IN (SELECT id FROM domain_events WHERE status IN ('pending','retry') AND available_at<=now() ORDER BY created_at LIMIT $1 FOR UPDATE SKIP LOCKED)
		RETURNING id,aggregate_type,aggregate_id,event_type,payload,attempts,created_at`, current.EventBatch)
	if err != nil {
		if ctx.Err() == nil {
			w.Logger.Error("claim events failed", "error", err)
		}
		return 0
	}
	events := make([]outboxEvent, 0, current.EventBatch)
	for rows.Next() {
		var event outboxEvent
		var raw []byte
		if rows.Scan(&event.ID, &event.AggregateType, &event.AggregateID, &event.EventType, &raw, &event.Attempts, &event.CreatedAt) != nil {
			continue
		}
		event.Payload = decodeObject(raw)
		events = append(events, event)
	}
	rows.Close()
	for _, event := range events {
		if ctx.Err() != nil {
			return len(events)
		}
		notified, err := w.handleEvent(ctx, current, event)
		if err != nil {
			w.failEvent(ctx, current, event, err)
			continue
		}
		if _, err := w.DB.Exec(ctx, `UPDATE domain_events SET status='done',processed_at=now(),last_error=NULL WHERE id=$1`, event.ID); err != nil && ctx.Err() == nil {
			w.Logger.Error("complete event failed", "error", err, "event_id", event.ID)
		}
		for _, userID := range notified {
			if w.Publish != nil {
				w.Publish(userID, map[string]any{"type": "notification.created", "event_type": event.EventType})
			}
		}
	}
	return len(events)
}

func (w *Worker) failEvent(ctx context.Context, current policy, event outboxEvent, cause error) {
	message := cause.Error()
	if len(message) > 500 {
		message = message[:500]
	}
	status := "retry"
	if event.Attempts >= current.EventRetryLimit {
		status = "failed"
	}
	w.Logger.Error("event dispatch failed", "error", message, "event_id", event.ID, "event_type", event.EventType, "attempts", event.Attempts, "status", status)
	if _, err := w.DB.Exec(context.WithoutCancel(ctx), `UPDATE domain_events SET status=$2,last_error=$3,available_at=now()+make_interval(secs => $4) WHERE id=$1`,
		event.ID, status, message, int(backoff(event.Attempts, time.Minute, 6*time.Hour)/time.Second)); err != nil {
		w.Logger.Error("record event failure failed", "error", err, "event_id", event.ID)
	}
}

func backoff(attempts int, base, max time.Duration) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	if attempts > 20 {
		attempts = 20
	}
	value := base << (attempts - 1)
	if value > max || value <= 0 {
		return max
	}
	return value
}

// handleEvent fans one event out to inbox notifications and webhook deliveries
// inside a single transaction and returns the users whose inbox changed.
func (w *Worker) handleEvent(ctx context.Context, current policy, event outboxEvent) ([]uuid.UUID, error) {
	tx, err := w.DB.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var notified []uuid.UUID
	if event.AggregateType == "webhook" {
		if err := w.queueDirectDelivery(ctx, tx, event); err != nil {
			return nil, err
		}
		return nil, tx.Commit(ctx)
	}
	subject, err := w.resolveSubject(ctx, tx, event)
	if err != nil {
		return nil, err
	}
	if current.WebChannel {
		notified, err = w.queueNotifications(ctx, tx, event, subject)
		if err != nil {
			return nil, err
		}
	}
	if err := w.queueMail(ctx, tx, current, event, subject); err != nil {
		return nil, err
	}
	if current.WebhookEnabled {
		if err := w.queueDeliveries(ctx, tx, event); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return notified, nil
}

// subject carries everything a template or a reader needs about the aggregate
// the event happened to.
type subject struct {
	Recipients []uuid.UUID
	Variables  map[string]string
	Link       string
	// Buyer and Seller are the two sides of anything that has both, so a mail
	// can be addressed to one side by role rather than by position.
	Buyer  uuid.UUID
	Seller uuid.UUID
}

func (w *Worker) resolveSubject(ctx context.Context, tx pgx.Tx, event outboxEvent) (subject, error) {
	result := subject{Variables: map[string]string{"event_type": event.EventType}}
	switch event.AggregateType {
	case "order":
		var buyer, seller uuid.UUID
		var number, title string
		err := tx.QueryRow(ctx, `SELECT o.buyer_id,o.seller_id,o.order_number,t.title FROM orders o JOIN talents t ON t.id=o.talent_id WHERE o.id=$1`, event.AggregateID).Scan(&buyer, &seller, &number, &title)
		if err != nil {
			return result, fmt.Errorf("resolve order %s: %w", event.AggregateID, err)
		}
		result.Recipients = []uuid.UUID{buyer, seller}
		result.Buyer, result.Seller = buyer, seller
		result.Variables["order_number"] = number
		result.Variables["talent_title"] = title
		result.Link = "/orders/" + event.AggregateID.String()
	case "talent":
		var seller uuid.UUID
		var title string
		err := tx.QueryRow(ctx, `SELECT seller_id,title FROM talents WHERE id=$1`, event.AggregateID).Scan(&seller, &title)
		if err != nil {
			return result, fmt.Errorf("resolve talent %s: %w", event.AggregateID, err)
		}
		result.Recipients = []uuid.UUID{seller}
		result.Variables["talent_title"] = title
		result.Link = "/talents/" + event.AggregateID.String()
	case "settlement":
		var seller, orderID uuid.UUID
		var number string
		var net int64
		err := tx.QueryRow(ctx, `SELECT s.seller_id,s.order_id,s.net_amount,o.order_number FROM settlements s JOIN orders o ON o.id=s.order_id WHERE s.id=$1`, event.AggregateID).Scan(&seller, &orderID, &net, &number)
		if err != nil {
			return result, fmt.Errorf("resolve settlement %s: %w", event.AggregateID, err)
		}
		result.Recipients = []uuid.UUID{seller}
		result.Seller = seller
		result.Variables["order_number"] = number
		result.Variables["net_amount"] = groupDigits(net)
		// The hold template ends in "사유: {{hold_reason}}", and nothing was
		// filling that in: a seller whose payout stopped was told it had
		// stopped and then shown an empty reason, which is the one piece of
		// the message they needed. The settlement row is read rather than the
		// payload so a reason set by any path reaches them.
		var holdReason *string
		if tx.QueryRow(ctx, `SELECT hold_reason FROM settlements WHERE id=$1`, event.AggregateID).Scan(&holdReason) == nil && holdReason != nil {
			result.Variables["hold_reason"] = *holdReason
		} else if reason, ok := event.Payload["hold_reason"].(string); ok {
			result.Variables["hold_reason"] = reason
		}
		result.Link = "/orders/" + orderID.String()
	case "dispute":
		var buyer, seller uuid.UUID
		var orderID uuid.UUID
		var number, title string
		err := tx.QueryRow(ctx, `SELECT o.id,o.buyer_id,o.seller_id,o.order_number,t.title FROM disputes d JOIN orders o ON o.id=d.order_id JOIN talents t ON t.id=o.talent_id WHERE d.id=$1`, event.AggregateID).Scan(&orderID, &buyer, &seller, &number, &title)
		if err != nil {
			return result, fmt.Errorf("resolve dispute %s: %w", event.AggregateID, err)
		}
		result.Recipients = []uuid.UUID{buyer, seller}
		result.Buyer, result.Seller = buyer, seller
		result.Variables["order_number"] = number
		result.Variables["talent_title"] = title
		result.Link = "/orders/" + orderID.String()
	case "report":
		var reporter uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT reporter_id FROM reports WHERE id=$1`, event.AggregateID).Scan(&reporter); err != nil {
			return result, fmt.Errorf("resolve report %s: %w", event.AggregateID, err)
		}
		result.Recipients = []uuid.UUID{reporter}
		result.Link = "/profile/reports"
	case "user":
		// The person the change happened to is the only recipient. These events
		// exist so that someone learns their own account changed under them.
		result.Recipients = []uuid.UUID{event.AggregateID}
		if roles, ok := event.Payload["roles"].(string); ok {
			result.Variables["roles"] = roles
		}
		if keyName, ok := event.Payload["key_name"].(string); ok {
			result.Variables["key_name"] = keyName
		}
		result.Link = "/profile/security"
	case "budget":
		// The people who can do something about it are the ones who manage the
		// money: owners and admins, not every member of the organization.
		var organizationID uuid.UUID
		var budgetName string
		if err := tx.QueryRow(ctx, `SELECT organization_id,name FROM budgets WHERE id=$1`, event.AggregateID).Scan(&organizationID, &budgetName); err != nil {
			return result, fmt.Errorf("resolve budget %s: %w", event.AggregateID, err)
		}
		rows, err := tx.Query(ctx, `SELECT user_id FROM organization_users WHERE organization_id=$1 AND role IN ('owner','admin')`, organizationID)
		if err != nil {
			return result, fmt.Errorf("resolve budget managers %s: %w", event.AggregateID, err)
		}
		defer rows.Close()
		for rows.Next() {
			var manager uuid.UUID
			if rows.Scan(&manager) == nil {
				result.Recipients = append(result.Recipients, manager)
			}
		}
		result.Variables["budget_name"] = budgetName
		result.Link = "/profile/organizations"
	case "inquiry":
		// Only the other party is told. The person who just typed the message
		// does not need to be informed that they typed it.
		var talentTitle string
		if err := tx.QueryRow(ctx, `SELECT t.title FROM inquiries i JOIN talents t ON t.id=i.talent_id WHERE i.id=$1`, event.AggregateID).Scan(&talentTitle); err != nil {
			return result, fmt.Errorf("resolve inquiry %s: %w", event.AggregateID, err)
		}
		if recipient, ok := parseUUIDValue(event.Payload["recipient"]); ok {
			result.Recipients = []uuid.UUID{recipient}
		}
		result.Variables["talent_title"] = talentTitle
		result.Link = "/profile/inquiries"
	case "rfq":
		var buyer uuid.UUID
		var title string
		err := tx.QueryRow(ctx, `SELECT buyer_id,title FROM rfqs WHERE id=$1`, event.AggregateID).Scan(&buyer, &title)
		if err != nil {
			return result, fmt.Errorf("resolve rfq %s: %w", event.AggregateID, err)
		}
		result.Recipients = []uuid.UUID{buyer}
		result.Buyer = buyer
		result.Variables["rfq_title"] = title
		result.Link = "/profile/projects"
	case "quote":
		var buyer, seller uuid.UUID
		var title string
		err := tx.QueryRow(ctx, `SELECT r.buyer_id,q.seller_id,r.title FROM quotes q JOIN rfqs r ON r.id=q.rfq_id WHERE q.id=$1`, event.AggregateID).Scan(&buyer, &seller, &title)
		if err != nil {
			return result, fmt.Errorf("resolve quote %s: %w", event.AggregateID, err)
		}
		// Both sides matter here: a new quote concerns the buyer, an accepted or
		// rejected one concerns the seller. Actor exclusion picks the right one.
		result.Recipients = []uuid.UUID{buyer, seller}
		result.Buyer, result.Seller = buyer, seller
		result.Variables["rfq_title"] = title
		result.Link = "/profile/projects"
	default:
		return result, nil
	}
	if recipient, ok := parseUUIDValue(event.Payload["recipient"]); ok {
		result.Recipients = []uuid.UUID{recipient}
	}
	for key, value := range event.Payload {
		if _, exists := result.Variables[key]; exists {
			continue
		}
		if text, ok := scalarText(value); ok {
			result.Variables[key] = text
		}
	}
	if actor, ok := parseUUIDValue(event.Payload["actor"]); ok {
		var name string
		if tx.QueryRow(ctx, `SELECT display_name FROM users WHERE id=$1`, actor).Scan(&name) == nil {
			result.Variables["actor_name"] = name
		}
	}
	return result, nil
}

func (w *Worker) queueNotifications(ctx context.Context, tx pgx.Tx, event outboxEvent, target subject) ([]uuid.UUID, error) {
	if len(target.Recipients) == 0 {
		return nil, nil
	}
	var subjectTemplate, bodyTemplate string
	var enabled bool
	err := tx.QueryRow(ctx, `SELECT subject_template,body_template,enabled FROM notification_templates WHERE key=$1 AND channel='web'`, event.EventType).Scan(&subjectTemplate, &bodyTemplate, &enabled)
	if err == pgx.ErrNoRows || (err == nil && !enabled) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	actor, hasActor := parseUUIDValue(event.Payload["actor"])
	seen := map[uuid.UUID]bool{}
	notified := make([]uuid.UUID, 0, len(target.Recipients))
	for _, recipient := range target.Recipients {
		if recipient == uuid.Nil || seen[recipient] || (hasActor && recipient == actor) {
			continue
		}
		seen[recipient] = true
		var wants bool
		err := tx.QueryRow(ctx, `SELECT enabled AND 'web'=ANY(channels) FROM notification_preferences WHERE user_id=$1 AND event_type=$2`, recipient, event.EventType).Scan(&wants)
		if err == nil && !wants {
			continue
		}
		if err != nil && err != pgx.ErrNoRows {
			return nil, err
		}
		tag, err := tx.Exec(ctx, `INSERT INTO notifications(id,user_id,channel,template_key,event_id,event_type,subject,body,data,link,state,sent_at)
			VALUES($1,$2,'web',$3,$4,$5,$6,$7,$8,$9,'sent',now()) ON CONFLICT DO NOTHING`,
			uuid.New(), recipient, event.EventType, event.ID, event.EventType,
			render(subjectTemplate, target.Variables), render(bodyTemplate, target.Variables), event.Payload, target.Link)
		if err != nil {
			return nil, err
		}
		if tag.RowsAffected() > 0 {
			notified = append(notified, recipient)
		}
	}
	return notified, nil
}

func (w *Worker) queueDeliveries(ctx context.Context, tx pgx.Tx, event outboxEvent) error {
	_, err := tx.Exec(ctx, `INSERT INTO webhook_deliveries(id,webhook_id,event_id)
		SELECT gen_random_uuid(),h.id,$1 FROM webhooks h WHERE h.enabled AND (h.events @> ARRAY[$2]::text[] OR h.events @> ARRAY['*']::text[])
		ON CONFLICT DO NOTHING`, event.ID, event.EventType)
	return err
}

func (w *Worker) queueDirectDelivery(ctx context.Context, tx pgx.Tx, event outboxEvent) error {
	_, err := tx.Exec(ctx, `INSERT INTO webhook_deliveries(id,webhook_id,event_id) SELECT gen_random_uuid(),h.id,$1 FROM webhooks h WHERE h.id=$2 AND h.enabled ON CONFLICT DO NOTHING`, event.ID, event.AggregateID)
	return err
}

func parseUUIDValue(value any) (uuid.UUID, bool) {
	text, ok := value.(string)
	if !ok {
		return uuid.Nil, false
	}
	parsed, err := uuid.Parse(text)
	if err != nil || parsed == uuid.Nil {
		return uuid.Nil, false
	}
	return parsed, true
}

func scalarText(value any) (string, bool) {
	switch typed := value.(type) {
	case string:
		return typed, true
	case bool:
		return strconv.FormatBool(typed), true
	case float64:
		if typed == float64(int64(typed)) {
			return groupDigits(int64(typed)), true
		}
		return strconv.FormatFloat(typed, 'f', -1, 64), true
	}
	return "", false
}

func groupDigits(value int64) string {
	text := strconv.FormatInt(value, 10)
	sign := ""
	if strings.HasPrefix(text, "-") {
		sign, text = "-", text[1:]
	}
	if len(text) <= 3 {
		return sign + text
	}
	var parts []string
	for len(text) > 3 {
		parts = append([]string{text[len(text)-3:]}, parts...)
		text = text[:len(text)-3]
	}
	return sign + text + "," + strings.Join(parts, ",")
}

// render substitutes {{name}} placeholders. Unknown placeholders collapse to an
// empty string so an operator edited template can never leak raw markup.
func render(template string, variables map[string]string) string {
	if !strings.Contains(template, "{{") {
		return template
	}
	var builder strings.Builder
	for {
		start := strings.Index(template, "{{")
		if start < 0 {
			builder.WriteString(template)
			return strings.TrimSpace(builder.String())
		}
		end := strings.Index(template[start:], "}}")
		if end < 0 {
			builder.WriteString(template)
			return strings.TrimSpace(builder.String())
		}
		end += start
		builder.WriteString(template[:start])
		builder.WriteString(variables[strings.TrimSpace(template[start+2:end])])
		template = template[end+2:]
	}
}

// flagOverdueOrders tells both sides when a paid order passes the date it was
// promised for. Nothing used to happen at all: the buyer's money stayed in
// escrow and the only way anyone learned the deadline had gone was by noticing
// it themselves. The event is emitted once per order, so a late order does not
// nag every scan.
func (w *Worker) flagOverdueOrders(ctx context.Context) int {
	rows, err := w.DB.Query(ctx, `SELECT o.id,o.due_at FROM orders o
		WHERE o.due_at IS NOT NULL AND o.due_at < now()
			AND o.state IN ('PAID','REQUIREMENT_PENDING','READY','IN_PROGRESS','REVISION_REQUESTED','CANCEL_REQUESTED')
			AND NOT EXISTS(SELECT 1 FROM domain_events e WHERE e.aggregate_id=o.id AND e.event_type='OrderOverdue')
		LIMIT 200`)
	if err != nil {
		return 0
	}
	type overdue struct {
		id  uuid.UUID
		due time.Time
	}
	pending := make([]overdue, 0)
	for rows.Next() {
		var row overdue
		if rows.Scan(&row.id, &row.due) == nil {
			pending = append(pending, row)
		}
	}
	rows.Close()
	flagged := 0
	for _, row := range pending {
		days := int(time.Since(row.due).Hours() / 24)
		_, err := w.DB.Exec(ctx, `INSERT INTO domain_events(id,aggregate_type,aggregate_id,event_type,payload)
			SELECT $1,'order',$2,'OrderOverdue',$3
			WHERE NOT EXISTS(SELECT 1 FROM domain_events e WHERE e.aggregate_id=$2 AND e.event_type='OrderOverdue')`,
			uuid.New(), row.id, map[string]any{"due_at": row.due, "overdue_days": days})
		if err == nil {
			flagged++
		}
	}
	if flagged > 0 {
		w.Logger.Info("orders passed their delivery date", "count", flagged)
	}
	return flagged
}
