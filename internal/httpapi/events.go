package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// emitEvent appends one record to the transactional outbox. The caller must be
// inside the same transaction as the state change so that a committed change
// always has its event and a rolled back change never leaves one behind.
func emitEvent(ctx context.Context, tx pgx.Tx, aggregateType string, aggregateID uuid.UUID, eventType string, payload map[string]any) error {
	if payload == nil {
		payload = map[string]any{}
	}
	_, err := tx.Exec(ctx, `INSERT INTO domain_events(id,aggregate_type,aggregate_id,event_type,payload) VALUES($1,$2,$3,$4,$5)`, uuid.New(), aggregateType, aggregateID, eventType, payload)
	return err
}

func queryLimit(r *http.Request, fallback, max int) int {
	value, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || value <= 0 {
		return fallback
	}
	if value > max {
		return max
	}
	return value
}

func (s *Server) listDomainEvents(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	eventType := r.URL.Query().Get("event_type")
	// domain_events is the fastest growing table in the product, so the filters
	// are only put in the predicate when they are actually set. An unset filter
	// written as `($1='' OR status=$1)` is invisible to the planner once the
	// statement is reused and a generic plan takes over.
	filters := ""
	if status != "" {
		filters += ` AND status=$1`
	}
	if eventType != "" {
		filters += ` AND event_type=$2`
	}
	rows, err := s.DB.Query(r.Context(), `SELECT id,aggregate_type,aggregate_id,event_type,payload,status,attempts,available_at,processed_at,last_error,created_at
		FROM domain_events WHERE true`+filters+` ORDER BY created_at DESC LIMIT $3`, status, eventType, queryLimit(r, 100, 500))
	if err != nil {
		writeError(w, 500, "query_failed", "이벤트를 조회하지 못했습니다.")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id, aggregateID uuid.UUID
		var aggregateType, eventName, state string
		var raw []byte
		var attempts int
		var lastError *string
		var availableAt, created time.Time
		var processed *time.Time
		if rows.Scan(&id, &aggregateType, &aggregateID, &eventName, &raw, &state, &attempts, &availableAt, &processed, &lastError, &created) != nil {
			continue
		}
		var payload any
		_ = json.Unmarshal(raw, &payload)
		items = append(items, map[string]any{"id": id, "aggregate_type": aggregateType, "aggregate_id": aggregateID, "event_type": eventName, "payload": payload, "status": state, "attempts": attempts, "available_at": availableAt, "processed_at": processed, "last_error": lastError, "created_at": created})
	}
	writeJSON(w, 200, map[string]any{"items": items, "summary": s.eventSummary(r.Context())})
}

func (s *Server) eventSummary(ctx context.Context) map[string]any {
	summary := map[string]any{}
	var pending, failed, done, deliveryPending, deliveryFailed, unread int64
	err := s.DB.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM domain_events WHERE status IN ('pending','retry','processing')),
		(SELECT count(*) FROM domain_events WHERE status='failed'),
		(SELECT count(*) FROM domain_events WHERE status='done'),
		(SELECT count(*) FROM webhook_deliveries WHERE state IN ('pending','retry')),
		(SELECT count(*) FROM webhook_deliveries WHERE state='failed'),
		(SELECT count(*) FROM notifications WHERE read_at IS NULL)`).Scan(&pending, &failed, &done, &deliveryPending, &deliveryFailed, &unread)
	if err != nil {
		return summary
	}
	summary["events_pending"] = pending
	summary["events_failed"] = failed
	summary["events_done"] = done
	summary["deliveries_pending"] = deliveryPending
	summary["deliveries_failed"] = deliveryFailed
	summary["notifications_unread"] = unread
	return summary
}

func (s *Server) retryDomainEvent(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDPath(w, r, "id")
	if !ok {
		return
	}
	tag, err := s.DB.Exec(r.Context(), `UPDATE domain_events SET status='pending',attempts=0,available_at=now(),last_error=NULL WHERE id=$1 AND status<>'done'`, id)
	if err != nil || tag.RowsAffected() == 0 {
		writeError(w, 404, "event_not_retryable", "재처리할 이벤트를 찾을 수 없습니다.")
		return
	}
	s.audit(r, "event.retry", "domain_event", id.String(), nil, nil, "success")
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) listAdminDeliveries(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	rows, err := s.DB.Query(r.Context(), `SELECT d.id,d.webhook_id,h.name,h.target_url,u.display_name,e.event_type,d.state,d.response_status,d.attempts,d.next_attempt_at,d.last_error,d.created_at,d.delivered_at
		FROM webhook_deliveries d JOIN webhooks h ON h.id=d.webhook_id JOIN users u ON u.id=h.owner_id JOIN domain_events e ON e.id=d.event_id
		WHERE ($1='' OR d.state=$1) ORDER BY d.created_at DESC LIMIT $2`, state, queryLimit(r, 100, 500))
	if err != nil {
		writeError(w, 500, "query_failed", "웹훅 전달 이력을 조회하지 못했습니다.")
		return
	}
	defer rows.Close()
	writeJSON(w, 200, map[string]any{"items": scanDeliveries(rows, true)})
}

func (s *Server) retryAdminDelivery(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDPath(w, r, "id")
	if !ok {
		return
	}
	if !s.requeueDelivery(r.Context(), id, uuid.Nil, uuid.Nil) {
		writeError(w, 404, "delivery_not_retryable", "재전송할 전달 기록을 찾을 수 없습니다.")
		return
	}
	s.audit(r, "webhook.delivery.retry", "webhook_delivery", id.String(), nil, nil, "success")
	writeJSON(w, 200, map[string]any{"ok": true})
}

// requeueDelivery resets one attempt. A zero owner means an operator retry and
// a zero webhook means the caller did not scope the retry to one webhook.
func (s *Server) requeueDelivery(ctx context.Context, id, owner, webhook uuid.UUID) bool {
	tag, err := s.DB.Exec(ctx, `UPDATE webhook_deliveries d SET state='pending',attempts=0,next_attempt_at=now(),last_error=NULL
		FROM webhooks h WHERE h.id=d.webhook_id AND d.id=$1 AND d.state<>'delivered'
		AND ($2::uuid IS NULL OR h.owner_id=$2) AND ($3::uuid IS NULL OR d.webhook_id=$3)`, id, nullableUUID(owner), nullableUUID(webhook))
	return err == nil && tag.RowsAffected() > 0
}
