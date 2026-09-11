package httpapi

import (
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/hkjang/Kkiit/internal/cryptox"
)

// eventCatalog is the contract shared by webhook subscriptions and per-user
// notification preferences. Every value is a domain event type written to the
// outbox by a business transaction.
var eventCatalog = []string{
	"OrderCreated", "OrderPAYMENT_PENDING", "OrderPAID", "OrderREQUIREMENT_PENDING", "OrderREADY", "OrderIN_PROGRESS",
	"OrderDELIVERED", "OrderREVISION_REQUESTED", "OrderACCEPTED", "OrderCOMPLETED", "OrderCANCEL_REQUESTED",
	"OrderCANCELLED", "OrderDISPUTED", "OrderREFUNDED", "OrderOverdue",
	"MessageCreated", "InquiryMessageCreated", "ReviewCreated", "DisputeOpened", "DisputeResolved",
	"TalentPublished", "TalentRejected", "TalentPaused", "ReportResolved", "SettlementCreated", "SettlementConfirmed", "SettlementHeld", "SettlementPaid",
	"RFQCreated", "QuoteCreated", "QuoteAccepted", "QuoteRejected", "OrganizationMemberAdded", "BudgetExhausted",
	"AccountMFAReset", "AccountRoleChanged", "AccountReactivated", "APIKeyRevoked",
}

func knownEvent(value string) bool {
	for _, item := range eventCatalog {
		if item == value {
			return true
		}
	}
	return false
}

type webhookInput struct {
	Name         string   `json:"name"`
	TargetURL    string   `json:"target_url"`
	Events       []string `json:"events"`
	Enabled      *bool    `json:"enabled,omitempty"`
	RotateSecret bool     `json:"rotate_secret,omitempty"`
}

func (s *Server) validateWebhookInput(r *http.Request, in *webhookInput) (string, bool) {
	allowPrivate := true
	if policy, err := s.settingObject(r, "notification.webhook"); err == nil {
		if allow, ok := policy["allow_private_targets"].(bool); ok {
			allowPrivate = allow
		}
	}
	return validateWebhookInput(in, allowPrivate)
}

func validateWebhookInput(in *webhookInput, allowPrivate bool) (string, bool) {
	in.Name = strings.TrimSpace(in.Name)
	in.TargetURL = strings.TrimSpace(in.TargetURL)
	if in.Name == "" || len(in.Name) > 100 {
		return "웹훅 이름은 1자 이상 100자 이하로 입력해 주세요.", false
	}
	target, err := url.Parse(in.TargetURL)
	if err != nil || target.Host == "" || (target.Scheme != "http" && target.Scheme != "https") {
		return "전달 주소는 http:// 또는 https://로 시작하는 URL이어야 합니다.", false
	}
	if len(in.TargetURL) > 500 {
		return "전달 주소가 너무 깁니다.", false
	}
	if len(in.Events) == 0 {
		return "구독할 이벤트를 하나 이상 선택해 주세요.", false
	}
	if len(in.Events) > len(eventCatalog) {
		return "구독 이벤트 수가 너무 많습니다.", false
	}
	for _, item := range in.Events {
		if item != "*" && !knownEvent(item) {
			return "지원하지 않는 이벤트입니다: " + item, false
		}
	}
	if !allowPrivate && isPrivateHost(target.Hostname()) {
		return "정책상 내부망 주소로는 웹훅을 등록할 수 없습니다.", false
	}
	return "", true
}

func (s *Server) listMyWebhooks(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	rows, err := s.DB.Query(r.Context(), `SELECT h.id,h.name,h.target_url,h.events,h.enabled,h.created_at,h.updated_at,
		(SELECT count(*) FROM webhook_deliveries d WHERE d.webhook_id=h.id AND d.state IN ('pending','retry')),
		(SELECT count(*) FROM webhook_deliveries d WHERE d.webhook_id=h.id AND d.state='failed'),
		(SELECT max(d.delivered_at) FROM webhook_deliveries d WHERE d.webhook_id=h.id AND d.state='delivered')
		FROM webhooks h WHERE h.owner_id=$1 ORDER BY h.created_at DESC`, p.UserID)
	if err != nil {
		writeError(w, 500, "query_failed", "웹훅을 조회하지 못했습니다.")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id uuid.UUID
		var name, target string
		var events []string
		var enabled bool
		var created, updated time.Time
		var pending, failed int64
		var lastDelivered *time.Time
		if rows.Scan(&id, &name, &target, &events, &enabled, &created, &updated, &pending, &failed, &lastDelivered) == nil {
			items = append(items, map[string]any{"id": id, "name": name, "target_url": target, "events": events, "enabled": enabled, "created_at": created, "updated_at": updated, "pending_deliveries": pending, "failed_deliveries": failed, "last_delivered_at": lastDelivered})
		}
	}
	writeJSON(w, 200, map[string]any{"items": items, "available_events": eventCatalog})
}

func (s *Server) createMyWebhook(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	var in webhookInput
	if !decodeJSON(w, r, &in) {
		return
	}
	if message, ok := s.validateWebhookInput(r, &in); !ok {
		writeError(w, 400, "invalid_webhook", message)
		return
	}
	secret, err := cryptox.RandomToken(32)
	if err != nil {
		writeError(w, 500, "secret_generation_failed", "웹훅 서명 키를 만들지 못했습니다.")
		return
	}
	id := uuid.New()
	encrypted, err := s.Box.Encrypt([]byte(secret), "webhook:"+id.String())
	if err != nil {
		writeError(w, 500, "encryption_failed", "웹훅 서명 키를 암호화하지 못했습니다.")
		return
	}
	enabled := in.Enabled == nil || *in.Enabled
	_, err = s.DB.Exec(r.Context(), `INSERT INTO webhooks(id,owner_id,name,target_url,events,secret_encrypted,enabled) VALUES($1,$2,$3,$4,$5,$6,$7)`, id, p.UserID, in.Name, in.TargetURL, in.Events, encrypted, enabled)
	if err != nil {
		writeError(w, 500, "create_failed", "웹훅을 저장하지 못했습니다.")
		return
	}
	s.audit(r, "webhook.create", "webhook", id.String(), nil, map[string]any{"name": in.Name, "target_url": in.TargetURL, "events": in.Events}, "success")
	writeJSON(w, 201, map[string]any{"id": id, "secret": secret, "warning": "이 서명 키는 다시 표시되지 않습니다."})
}

func (s *Server) updateMyWebhook(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	id, ok := parseUUIDPath(w, r, "id")
	if !ok {
		return
	}
	var in webhookInput
	if !decodeJSON(w, r, &in) {
		return
	}
	if message, valid := s.validateWebhookInput(r, &in); !valid {
		writeError(w, 400, "invalid_webhook", message)
		return
	}
	enabled := in.Enabled == nil || *in.Enabled
	var secret string
	var encrypted any
	if in.RotateSecret {
		value, err := cryptox.RandomToken(32)
		if err != nil {
			writeError(w, 500, "secret_generation_failed", "웹훅 서명 키를 만들지 못했습니다.")
			return
		}
		cipher, err := s.Box.Encrypt([]byte(value), "webhook:"+id.String())
		if err != nil {
			writeError(w, 500, "encryption_failed", "웹훅 서명 키를 암호화하지 못했습니다.")
			return
		}
		secret, encrypted = value, cipher
	}
	tag, err := s.DB.Exec(r.Context(), `UPDATE webhooks SET name=$3,target_url=$4,events=$5,enabled=$6,secret_encrypted=COALESCE($7,secret_encrypted),updated_at=now() WHERE id=$1 AND owner_id=$2`, id, p.UserID, in.Name, in.TargetURL, in.Events, enabled, encrypted)
	if err != nil || tag.RowsAffected() == 0 {
		writeError(w, 404, "webhook_not_found", "웹훅을 찾을 수 없습니다.")
		return
	}
	s.audit(r, "webhook.update", "webhook", id.String(), nil, map[string]any{"name": in.Name, "target_url": in.TargetURL, "events": in.Events, "enabled": enabled, "rotated": in.RotateSecret}, "success")
	response := map[string]any{"ok": true}
	if secret != "" {
		response["secret"] = secret
		response["warning"] = "이 서명 키는 다시 표시되지 않습니다."
	}
	writeJSON(w, 200, response)
}

func (s *Server) deleteMyWebhook(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	id, ok := parseUUIDPath(w, r, "id")
	if !ok {
		return
	}
	tag, err := s.DB.Exec(r.Context(), `DELETE FROM webhooks WHERE id=$1 AND owner_id=$2`, id, p.UserID)
	if err != nil || tag.RowsAffected() == 0 {
		writeError(w, 404, "webhook_not_found", "웹훅을 찾을 수 없습니다.")
		return
	}
	s.audit(r, "webhook.delete", "webhook", id.String(), nil, nil, "success")
	w.WriteHeader(204)
}

// testMyWebhook writes a synthetic outbox event addressed to a single webhook.
// The dispatcher delivers it through the same signing and retry path as a real
// business event, so a successful test proves the whole chain.
func (s *Server) testMyWebhook(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	id, ok := parseUUIDPath(w, r, "id")
	if !ok {
		return
	}
	var enabled bool
	if err := s.DB.QueryRow(r.Context(), `SELECT enabled FROM webhooks WHERE id=$1 AND owner_id=$2`, id, p.UserID).Scan(&enabled); err != nil {
		writeError(w, 404, "webhook_not_found", "웹훅을 찾을 수 없습니다.")
		return
	}
	if !enabled {
		writeError(w, 409, "webhook_disabled", "비활성 웹훅은 테스트할 수 없습니다.")
		return
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		writeError(w, 500, "transaction_failed", "테스트 전송을 시작하지 못했습니다.")
		return
	}
	defer tx.Rollback(r.Context()) //nolint:errcheck
	if err = emitEvent(r.Context(), tx, "webhook", id, "WebhookTest", map[string]any{"requested_by": p.UserID}); err != nil {
		writeError(w, 500, "test_failed", "테스트 이벤트를 만들지 못했습니다.")
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		writeError(w, 500, "test_failed", "테스트 이벤트를 만들지 못했습니다.")
		return
	}
	writeJSON(w, 202, map[string]any{"ok": true, "message": "테스트 이벤트를 큐에 넣었습니다. 전달 이력에서 결과를 확인하세요."})
}

func (s *Server) listMyDeliveries(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	id, ok := parseUUIDPath(w, r, "id")
	if !ok {
		return
	}
	rows, err := s.DB.Query(r.Context(), `SELECT d.id,d.webhook_id,e.event_type,d.state,d.response_status,d.attempts,d.next_attempt_at,d.last_error,d.created_at,d.delivered_at
		FROM webhook_deliveries d JOIN webhooks h ON h.id=d.webhook_id JOIN domain_events e ON e.id=d.event_id
		WHERE d.webhook_id=$1 AND h.owner_id=$2 ORDER BY d.created_at DESC LIMIT $3`, id, p.UserID, queryLimit(r, 50, 200))
	if err != nil {
		writeError(w, 500, "query_failed", "전달 이력을 조회하지 못했습니다.")
		return
	}
	defer rows.Close()
	writeJSON(w, 200, map[string]any{"items": scanDeliveries(rows, false)})
}

func (s *Server) retryMyDelivery(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	webhookID, ok := parseUUIDPath(w, r, "id")
	if !ok {
		return
	}
	id, ok := parseUUIDPath(w, r, "deliveryId")
	if !ok {
		return
	}
	if !s.requeueDelivery(r.Context(), id, p.UserID, webhookID) {
		writeError(w, 404, "delivery_not_retryable", "재전송할 전달 기록을 찾을 수 없습니다.")
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func scanDeliveries(rows pgx.Rows, withOwner bool) []map[string]any {
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id, webhookID uuid.UUID
		var name, target, owner string
		var eventType, state string
		var responseStatus *int32
		var attempts int
		var lastError *string
		var next, created time.Time
		var delivered *time.Time
		var err error
		if withOwner {
			err = rows.Scan(&id, &webhookID, &name, &target, &owner, &eventType, &state, &responseStatus, &attempts, &next, &lastError, &created, &delivered)
		} else {
			err = rows.Scan(&id, &webhookID, &eventType, &state, &responseStatus, &attempts, &next, &lastError, &created, &delivered)
		}
		if err != nil {
			continue
		}
		item := map[string]any{"id": id, "webhook_id": webhookID, "event_type": eventType, "state": state, "response_status": responseStatus, "attempts": attempts, "next_attempt_at": next, "last_error": lastError, "created_at": created, "delivered_at": delivered}
		if withOwner {
			item["webhook_name"] = name
			item["target_url"] = target
			item["owner_name"] = owner
		}
		items = append(items, item)
	}
	return items
}
