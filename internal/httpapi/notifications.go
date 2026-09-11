package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"

	"github.com/hkjang/Kkiit/internal/netguard"
)

func isPrivateHost(host string) bool { return netguard.IsPrivateHost(host) }

func (s *Server) listMyNotifications(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	unreadOnly := r.URL.Query().Get("unread") == "true"
	limit := queryLimit(r, 30, 200)
	cursorAt, cursorID := requestCursor(r)
	rows, err := s.DB.Query(r.Context(), `SELECT id,event_type,template_key,subject,body,data,link,read_at,created_at
		FROM notifications WHERE user_id=$1 AND channel='web' AND ($2=false OR read_at IS NULL)
		AND ($4::timestamptz IS NULL OR (created_at,id) < ($4,$5)) ORDER BY created_at DESC,id DESC LIMIT $3`, p.UserID, unreadOnly, limit+1, cursorAt, cursorID)
	if err != nil {
		writeError(w, 500, "query_failed", "알림을 조회하지 못했습니다.")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id uuid.UUID
		var eventType, templateKey, subject, body, link string
		var raw []byte
		var readAt *time.Time
		var created time.Time
		if rows.Scan(&id, &eventType, &templateKey, &subject, &body, &raw, &link, &readAt, &created) != nil {
			continue
		}
		var data any
		_ = json.Unmarshal(raw, &data)
		items = append(items, map[string]any{"id": id, "event_type": eventType, "template_key": templateKey, "subject": subject, "body": body, "data": data, "link": link, "read_at": readAt, "created_at": created})
	}
	items, next := pageResult(items, limit, func(item map[string]any) (time.Time, uuid.UUID) {
		return timeField(item, "created_at"), uuidField(item, "id")
	})
	writeJSON(w, 200, map[string]any{"items": items, "next_cursor": next, "unread_count": s.unreadCount(r.Context(), p.UserID)})
}

func (s *Server) unreadCount(ctx context.Context, userID uuid.UUID) int64 {
	var count int64
	if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE user_id=$1 AND channel='web' AND read_at IS NULL`, userID).Scan(&count); err != nil {
		return 0
	}
	return count
}

func (s *Server) markNotificationsRead(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	var in struct {
		IDs []uuid.UUID `json:"ids"`
		All bool        `json:"all"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if !in.All && len(in.IDs) == 0 {
		writeError(w, 400, "invalid_request", "읽음 처리할 알림을 지정해 주세요.")
		return
	}
	_, err := s.DB.Exec(r.Context(), `UPDATE notifications SET read_at=now() WHERE user_id=$1 AND read_at IS NULL AND ($2 OR id=ANY($3))`, p.UserID, in.All, in.IDs)
	if err != nil {
		writeError(w, 500, "update_failed", "알림을 읽음 처리하지 못했습니다.")
		return
	}
	writeJSON(w, 200, map[string]any{"unread_count": s.unreadCount(r.Context(), p.UserID)})
}

func (s *Server) listMyNotificationPreferences(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	stored := map[string]map[string]any{}
	rows, err := s.DB.Query(r.Context(), `SELECT event_type,channels,enabled FROM notification_preferences WHERE user_id=$1`, p.UserID)
	if err != nil {
		writeError(w, 500, "query_failed", "알림 설정을 조회하지 못했습니다.")
		return
	}
	for rows.Next() {
		var eventType string
		var channels []string
		var enabled bool
		if rows.Scan(&eventType, &channels, &enabled) == nil {
			stored[eventType] = map[string]any{"event_type": eventType, "channels": channels, "enabled": enabled}
		}
	}
	rows.Close()
	items := make([]map[string]any, 0, len(eventCatalog))
	for _, eventType := range eventCatalog {
		if item, ok := stored[eventType]; ok {
			items = append(items, item)
			continue
		}
		items = append(items, map[string]any{"event_type": eventType, "channels": []string{"web"}, "enabled": true})
	}
	channels, _ := s.settingObject(r, "notification.channels")
	writeJSON(w, 200, map[string]any{"items": items, "enabled_channels": channels})
}

func (s *Server) putMyNotificationPreferences(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	var in struct {
		Items []struct {
			EventType string   `json:"event_type"`
			Channels  []string `json:"channels"`
			Enabled   bool     `json:"enabled"`
		} `json:"items"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		writeError(w, 500, "transaction_failed", "알림 설정을 저장하지 못했습니다.")
		return
	}
	defer tx.Rollback(r.Context()) //nolint:errcheck
	for _, item := range in.Items {
		if !knownEvent(item.EventType) {
			writeError(w, 400, "unknown_event", "지원하지 않는 이벤트입니다: "+item.EventType)
			return
		}
		channels := item.Channels
		if len(channels) == 0 {
			channels = []string{"web"}
		}
		if _, err = tx.Exec(r.Context(), `INSERT INTO notification_preferences(user_id,event_type,channels,enabled) VALUES($1,$2,$3,$4)
			ON CONFLICT(user_id,event_type) DO UPDATE SET channels=EXCLUDED.channels,enabled=EXCLUDED.enabled`, p.UserID, item.EventType, channels, item.Enabled); err != nil {
			writeError(w, 500, "update_failed", "알림 설정을 저장하지 못했습니다.")
			return
		}
	}
	if err = tx.Commit(r.Context()); err != nil {
		writeError(w, 500, "update_failed", "알림 설정을 저장하지 못했습니다.")
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

// notificationWebSocket streams inbox updates for the signed in user so the
// header badge reflects a state change without polling.
func (s *Server) notificationWebSocket(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	if err := clearDeadlines(w); err != nil {
		s.Logger.Warn("연결 타임아웃을 해제하지 못했습니다", "error", err, "path", r.URL.Path)
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return
	}
	defer conn.CloseNow() //nolint:errcheck
	conn.SetReadLimit(2048)
	updates, unsubscribe := s.subscribe(userTopic(p.UserID))
	defer unsubscribe()
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	go func() {
		for {
			if _, _, err := conn.Read(ctx); err != nil {
				cancel()
				return
			}
		}
	}()
	ready, _ := json.Marshal(map[string]any{"type": "connected", "unread_count": s.unreadCount(ctx, p.UserID)})
	if err := conn.Write(ctx, websocket.MessageText, ready); err != nil {
		return
	}
	keepalive := time.NewTicker(30 * time.Second)
	defer keepalive.Stop()
	for {
		select {
		case payload := <-updates:
			if err := conn.Write(ctx, websocket.MessageText, payload); err != nil {
				return
			}
		case <-keepalive.C:
			if err := conn.Ping(ctx); err != nil {
				return
			}
		case <-ctx.Done():
			return
		}
	}
}

func (s *Server) listNotificationTemplates(w http.ResponseWriter, r *http.Request) {
	rows, err := s.DB.Query(r.Context(), `SELECT key,channel,locale,subject_template,body_template,enabled,version,updated_at FROM notification_templates ORDER BY key`)
	if err != nil {
		writeError(w, 500, "query_failed", "알림 템플릿을 조회하지 못했습니다.")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var key, channel, locale, subject, body string
		var enabled bool
		var version int
		var updated time.Time
		if rows.Scan(&key, &channel, &locale, &subject, &body, &enabled, &version, &updated) == nil {
			items = append(items, map[string]any{"key": key, "channel": channel, "locale": locale, "subject_template": subject, "body_template": body, "enabled": enabled, "version": version, "updated_at": updated})
		}
	}
	writeJSON(w, 200, map[string]any{"items": items, "available_events": eventCatalog})
}

func (s *Server) putNotificationTemplate(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	key := strings.TrimSpace(r.PathValue("key"))
	var in struct {
		Channel         string `json:"channel"`
		Locale          string `json:"locale"`
		SubjectTemplate string `json:"subject_template"`
		BodyTemplate    string `json:"body_template"`
		Enabled         bool   `json:"enabled"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	in.BodyTemplate = strings.TrimSpace(in.BodyTemplate)
	if key == "" || in.BodyTemplate == "" || len(in.BodyTemplate) > 2000 || len(in.SubjectTemplate) > 200 {
		writeError(w, 400, "invalid_template", "템플릿 본문을 1자 이상 2,000자 이하로 입력해 주세요.")
		return
	}
	if in.Channel == "" {
		in.Channel = "web"
	}
	if in.Locale == "" {
		in.Locale = "ko-KR"
	}
	_, err := s.DB.Exec(r.Context(), `INSERT INTO notification_templates(key,channel,locale,subject_template,body_template,enabled,updated_by)
		VALUES($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT(key) DO UPDATE SET channel=EXCLUDED.channel,locale=EXCLUDED.locale,subject_template=EXCLUDED.subject_template,body_template=EXCLUDED.body_template,enabled=EXCLUDED.enabled,version=notification_templates.version+1,updated_by=EXCLUDED.updated_by,updated_at=now()`,
		key, in.Channel, in.Locale, in.SubjectTemplate, in.BodyTemplate, in.Enabled, p.UserID)
	if err != nil {
		writeError(w, 500, "update_failed", "알림 템플릿을 저장하지 못했습니다.")
		return
	}
	s.audit(r, "notification.template.update", "notification_template", key, nil, in, "success")
	writeJSON(w, 200, map[string]any{"ok": true})
}
