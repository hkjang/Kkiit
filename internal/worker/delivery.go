package worker

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/hkjang/Kkiit/internal/netguard"
)

const deliveryConcurrency = 6

type pendingDelivery struct {
	ID            uuid.UUID
	WebhookID     uuid.UUID
	TargetURL     string
	Secret        []byte
	Attempts      int
	EventID       uuid.UUID
	EventType     string
	AggregateType string
	AggregateID   uuid.UUID
	Payload       json.RawMessage
	OccurredAt    time.Time
}

func (w *Worker) deliverWebhooks(ctx context.Context, current policy) int {
	if !current.WebhookEnabled {
		return 0
	}
	rows, err := w.DB.Query(ctx, `WITH claimed AS (
			UPDATE webhook_deliveries SET state='sending',attempts=attempts+1
			WHERE id IN (SELECT id FROM webhook_deliveries WHERE state IN ('pending','retry') AND next_attempt_at<=now() ORDER BY next_attempt_at LIMIT $1 FOR UPDATE SKIP LOCKED)
			RETURNING id,webhook_id,event_id,attempts)
		SELECT c.id,c.webhook_id,h.target_url,h.secret_encrypted,c.attempts,e.id,e.event_type,e.aggregate_type,e.aggregate_id,e.payload,e.created_at
		FROM claimed c JOIN webhooks h ON h.id=c.webhook_id JOIN domain_events e ON e.id=c.event_id`, current.DeliveryBatch)
	if err != nil {
		if ctx.Err() == nil {
			w.Logger.Error("claim deliveries failed", "error", err)
		}
		return 0
	}
	items := make([]pendingDelivery, 0, current.DeliveryBatch)
	for rows.Next() {
		var item pendingDelivery
		var payload []byte
		if rows.Scan(&item.ID, &item.WebhookID, &item.TargetURL, &item.Secret, &item.Attempts, &item.EventID, &item.EventType, &item.AggregateType, &item.AggregateID, &payload, &item.OccurredAt) != nil {
			continue
		}
		item.Payload = payload
		items = append(items, item)
	}
	rows.Close()
	if len(items) == 0 {
		return 0
	}
	client := netguard.Client(current.Timeout, current.AllowPrivate)
	queue := make(chan pendingDelivery)
	var group sync.WaitGroup
	for i := 0; i < deliveryConcurrency; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			for item := range queue {
				w.sendDelivery(ctx, current, client, item)
			}
		}()
	}
	for _, item := range items {
		queue <- item
	}
	close(queue)
	group.Wait()
	return len(items)
}

func (w *Worker) sendDelivery(ctx context.Context, current policy, client *http.Client, item pendingDelivery) {
	status, err := w.postDelivery(ctx, client, item)
	// The result must be recorded even while shutting down, otherwise the row
	// stays in sending until the stuck sweeper reclaims it.
	recordCtx := context.WithoutCancel(ctx)
	if err == nil {
		if _, dbErr := w.DB.Exec(recordCtx, `UPDATE webhook_deliveries SET state='delivered',response_status=$2,delivered_at=now(),last_error=NULL WHERE id=$1`, item.ID, status); dbErr != nil {
			w.Logger.Error("record delivery success failed", "error", dbErr, "delivery_id", item.ID)
		}
		return
	}
	message := err.Error()
	if len(message) > 500 {
		message = message[:500]
	}
	state := "retry"
	if item.Attempts >= current.MaxAttempts {
		state = "failed"
	}
	w.Logger.Warn("webhook delivery failed", "error", message, "delivery_id", item.ID, "webhook_id", item.WebhookID, "attempts", item.Attempts, "state", state)
	var responseStatus any
	if status > 0 {
		responseStatus = status
	}
	if _, dbErr := w.DB.Exec(recordCtx, `UPDATE webhook_deliveries SET state=$2,response_status=$3,last_error=$4,next_attempt_at=now()+make_interval(secs => $5) WHERE id=$1`,
		item.ID, state, responseStatus, message, int(backoff(item.Attempts, 30*time.Second, 6*time.Hour)/time.Second)); dbErr != nil {
		w.Logger.Error("record delivery failure failed", "error", dbErr, "delivery_id", item.ID)
	}
}

func (w *Worker) postDelivery(ctx context.Context, client *http.Client, item pendingDelivery) (int, error) {
	secret, err := w.Box.Decrypt(item.Secret, "webhook:"+item.WebhookID.String())
	if err != nil {
		return 0, fmt.Errorf("서명 키를 복호화하지 못했습니다: %w", err)
	}
	body, err := json.Marshal(map[string]any{
		"delivery_id": item.ID, "event_id": item.EventID, "event_type": item.EventType,
		"aggregate_type": item.AggregateType, "aggregate_id": item.AggregateID,
		"occurred_at": item.OccurredAt.UTC().Format(time.RFC3339), "payload": item.Payload,
	})
	if err != nil {
		return 0, err
	}
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(timestamp))
	mac.Write([]byte("."))
	mac.Write(body)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, item.TargetURL, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	request.Header.Set("Content-Type", "application/json; charset=utf-8")
	request.Header.Set("User-Agent", "Kkiit-Webhook/1")
	request.Header.Set("X-Kkiit-Event", item.EventType)
	request.Header.Set("X-Kkiit-Delivery", item.ID.String())
	request.Header.Set("X-Kkiit-Webhook", item.WebhookID.String())
	request.Header.Set("X-Kkiit-Timestamp", timestamp)
	request.Header.Set("X-Kkiit-Signature", "t="+timestamp+",v1="+hex.EncodeToString(mac.Sum(nil)))
	response, err := client.Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return response.StatusCode, fmt.Errorf("대상이 HTTP %d로 응답했습니다", response.StatusCode)
	}
	return response.StatusCode, nil
}
