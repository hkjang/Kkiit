package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *Server) orderAccess(r *http.Request, orderID uuid.UUID, p Principal) (buyer, seller uuid.UUID, state string, allowed bool) {
	if err := s.DB.QueryRow(r.Context(), `SELECT buyer_id,seller_id,state FROM orders WHERE id=$1`, orderID).Scan(&buyer, &seller, &state); err != nil {
		return uuid.Nil, uuid.Nil, "", false
	}
	return buyer, seller, state, p.UserID == buyer || p.UserID == seller || hasPermission(p, "orders.manage")
}

func (s *Server) listMessages(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	id, ok := parseUUIDPath(w, r, "id")
	if !ok {
		return
	}
	_, _, _, allowed := s.orderAccess(r, id, p)
	if !allowed {
		writeError(w, 403, "order_access_denied", "이 주문의 대화를 볼 수 없습니다.")
		return
	}
	rows, err := s.DB.Query(r.Context(), `SELECT m.id,m.sender_id,u.display_name,m.message_type,m.body,m.attachments,m.read_at,m.created_at FROM messages m LEFT JOIN users u ON u.id=m.sender_id WHERE m.order_id=$1 ORDER BY m.created_at ASC LIMIT 500`, id)
	if err != nil {
		writeError(w, 500, "query_failed", "메시지를 조회하지 못했습니다.")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var messageID uuid.UUID
		var sender *uuid.UUID
		var display *string
		var typ, body string
		var attachments []byte
		var readAt *time.Time
		var created time.Time
		if rows.Scan(&messageID, &sender, &display, &typ, &body, &attachments, &readAt, &created) == nil {
			var attachmentItems any
			_ = json.Unmarshal(attachments, &attachmentItems)
			items = append(items, map[string]any{"id": messageID, "sender_id": sender, "sender_name": display, "message_type": typ, "body": body, "attachments": attachmentItems, "read_at": readAt, "created_at": created})
		}
	}
	_, _ = s.DB.Exec(r.Context(), `UPDATE messages SET read_at=now() WHERE order_id=$1 AND sender_id IS DISTINCT FROM $2 AND read_at IS NULL`, id, p.UserID)
	writeJSON(w, 200, map[string]any{"items": items})
}

func (s *Server) createMessage(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	id, ok := parseUUIDPath(w, r, "id")
	if !ok {
		return
	}
	buyer, seller, _, allowed := s.orderAccess(r, id, p)
	if !allowed {
		writeError(w, 403, "order_access_denied", "이 주문에 메시지를 보낼 수 없습니다.")
		return
	}
	var in struct {
		Body        string           `json:"body"`
		Attachments []map[string]any `json:"attachments"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	in.Body = strings.TrimSpace(in.Body)
	if in.Body == "" || len([]rune(in.Body)) > 10000 {
		writeError(w, 400, "invalid_message", "메시지는 1자 이상 10,000자 이하로 입력해 주세요.")
		return
	}
	messageID := uuid.New()
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		writeError(w, 500, "message_failed", "메시지를 보내지 못했습니다.")
		return
	}
	defer tx.Rollback(r.Context()) //nolint:errcheck
	_, err = tx.Exec(r.Context(), `INSERT INTO messages(id,order_id,sender_id,body,attachments) VALUES($1,$2,$3,$4,$5)`, messageID, id, p.UserID, in.Body, in.Attachments)
	if err == nil {
		recipient := buyer
		if p.UserID == buyer {
			recipient = seller
		}
		_, err = tx.Exec(r.Context(), `INSERT INTO notifications(id,user_id,channel,template_key,body,data) VALUES($1,$2,'web','order.message',$3,$4)`, uuid.New(), recipient, "새 주문 메시지가 도착했습니다.", map[string]any{"order_id": id, "message_id": messageID})
	}
	if err != nil {
		writeError(w, 500, "message_failed", "메시지를 보내지 못했습니다.")
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		writeError(w, 500, "message_failed", "메시지를 보내지 못했습니다.")
		return
	}
	s.broadcastOrder(id, map[string]any{"type": "message.created", "order_id": id, "message_id": messageID})
	writeJSON(w, 201, map[string]any{"id": messageID, "created_at": time.Now()})
}

func (s *Server) messageWebSocket(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	id, ok := parseUUIDPath(w, r, "id")
	if !ok {
		return
	}
	_, _, _, allowed := s.orderAccess(r, id, p)
	if !allowed {
		writeError(w, 403, "order_access_denied", "이 주문의 실시간 대화를 볼 수 없습니다.")
		return
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return
	}
	defer conn.CloseNow() //nolint:errcheck
	conn.SetReadLimit(2048)
	updates, unsubscribe := s.subscribeOrder(id)
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
	ready, _ := json.Marshal(map[string]any{"type": "connected", "order_id": id})
	if err := conn.Write(ctx, websocket.MessageText, ready); err != nil {
		return
	}
	for {
		select {
		case payload := <-updates:
			if err := conn.Write(ctx, websocket.MessageText, payload); err != nil {
				return
			}
		case <-ctx.Done():
			return
		}
	}
}

func (s *Server) subscribeOrder(id uuid.UUID) (chan []byte, func()) {
	updates := make(chan []byte, 16)
	s.hubMu.Lock()
	if s.orderSubscribers == nil {
		s.orderSubscribers = make(map[uuid.UUID]map[chan []byte]struct{})
	}
	if s.orderSubscribers[id] == nil {
		s.orderSubscribers[id] = make(map[chan []byte]struct{})
	}
	s.orderSubscribers[id][updates] = struct{}{}
	s.hubMu.Unlock()
	return updates, func() {
		s.hubMu.Lock()
		delete(s.orderSubscribers[id], updates)
		if len(s.orderSubscribers[id]) == 0 {
			delete(s.orderSubscribers, id)
		}
		s.hubMu.Unlock()
	}
}

func (s *Server) broadcastOrder(id uuid.UUID, event any) {
	payload, err := json.Marshal(event)
	if err != nil {
		return
	}
	s.hubMu.Lock()
	defer s.hubMu.Unlock()
	for subscriber := range s.orderSubscribers[id] {
		select {
		case subscriber <- payload:
		default:
		}
	}
}

func (s *Server) createReview(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	id, ok := parseUUIDPath(w, r, "id")
	if !ok {
		return
	}
	var in struct {
		Quality         int16  `json:"quality"`
		Communication   int16  `json:"communication"`
		Timeliness      int16  `json:"timeliness"`
		Professionalism int16  `json:"professionalism"`
		Repurchase      bool   `json:"repurchase"`
		Body            string `json:"body"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.Quality < 1 || in.Quality > 5 || in.Communication < 1 || in.Communication > 5 || in.Timeliness < 1 || in.Timeliness > 5 || in.Professionalism < 1 || in.Professionalism > 5 {
		writeError(w, 400, "invalid_review", "모든 평가 항목은 1~5점이어야 합니다.")
		return
	}
	tx, err := s.DB.BeginTx(r.Context(), pgx.TxOptions{})
	if err != nil {
		writeError(w, 500, "review_failed", "리뷰 작성을 시작하지 못했습니다.")
		return
	}
	defer tx.Rollback(r.Context()) //nolint:errcheck
	var buyer, seller uuid.UUID
	var state string
	err = tx.QueryRow(r.Context(), `SELECT buyer_id,seller_id,state FROM orders WHERE id=$1 FOR UPDATE`, id).Scan(&buyer, &seller, &state)
	if err != nil || buyer != p.UserID {
		writeError(w, 403, "review_denied", "이 주문을 평가할 수 없습니다.")
		return
	}
	if state != "ACCEPTED" && state != "COMPLETED" {
		writeError(w, 409, "review_too_early", "구매확정 후 리뷰를 작성할 수 있습니다.")
		return
	}
	reviewID := uuid.New()
	_, err = tx.Exec(r.Context(), `INSERT INTO reviews(id,order_id,buyer_id,seller_id,quality,communication,timeliness,professionalism,repurchase,body) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, reviewID, id, buyer, seller, in.Quality, in.Communication, in.Timeliness, in.Professionalism, in.Repurchase, strings.TrimSpace(in.Body))
	if err == nil {
		_, err = tx.Exec(r.Context(), `UPDATE seller_profiles SET score=(SELECT avg((quality+communication+timeliness+professionalism)::numeric/4) FROM reviews WHERE seller_id=$1),updated_at=now() WHERE user_id=$1`, seller)
	}
	if err == nil && state == "ACCEPTED" {
		err = s.applyOrderTransition(r, tx, id, p.UserID, "ACCEPTED", "COMPLETED", map[string]any{"review_id": reviewID})
	}
	if err != nil {
		writeError(w, 409, "review_exists", "이 주문의 리뷰가 이미 있거나 저장할 수 없습니다.")
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		writeError(w, 500, "review_failed", "리뷰를 저장하지 못했습니다.")
		return
	}
	s.audit(r, "review.create", "review", reviewID.String(), nil, map[string]any{"order_id": id}, "success")
	writeJSON(w, 201, map[string]any{"id": reviewID, "order_state": "COMPLETED"})
}
