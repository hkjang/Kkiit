package httpapi

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const maxInquiryBody = 2000

// startInquiry is the conversation that decides whether there will be an order.
// It is deliberately not an order: no escrow, no deadline, no state machine.
// A buyer asks whether the package includes the source files, the seller
// answers, and either it becomes an order or it does not.
func (s *Server) startInquiry(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	talentID, ok := parseUUIDPath(w, r, "id")
	if !ok {
		return
	}
	body, ok := decodeInquiryBody(w, r)
	if !ok {
		return
	}
	tx, err := s.DB.BeginTx(r.Context(), pgx.TxOptions{})
	if err != nil {
		writeError(w, 500, "transaction_failed", "문의를 시작하지 못했습니다.")
		return
	}
	defer tx.Rollback(r.Context()) //nolint:errcheck
	var seller uuid.UUID
	var title string
	if err := tx.QueryRow(r.Context(), `SELECT t.seller_id,t.title FROM talents t JOIN users u ON u.id=t.seller_id
		WHERE t.id=$1 AND t.status='published' AND u.status='active'`, talentID).Scan(&seller, &title); err != nil {
		writeError(w, 404, "talent_not_available", "문의할 수 있는 상품이 아닙니다.")
		return
	}
	if seller == p.UserID {
		writeError(w, 400, "self_inquiry_not_allowed", "본인의 상품에는 문의할 수 없습니다.")
		return
	}
	// A buyer who asks a second question is continuing the same thread rather
	// than opening another one the seller has to notice separately.
	var inquiryID uuid.UUID
	err = tx.QueryRow(r.Context(), `INSERT INTO inquiries(id,talent_id,buyer_id,seller_id) VALUES($1,$2,$3,$4)
		ON CONFLICT (talent_id,buyer_id) DO UPDATE SET last_message_at=now(),state='open' RETURNING id`,
		uuid.New(), talentID, p.UserID, seller).Scan(&inquiryID)
	if err != nil {
		writeError(w, 500, "inquiry_failed", "문의를 시작하지 못했습니다.")
		return
	}
	if err := appendInquiryMessage(r.Context(), tx, inquiryID, p.UserID, body, title, seller); err != nil {
		writeError(w, 500, "inquiry_failed", "문의를 저장하지 못했습니다.")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, 500, "inquiry_failed", "문의를 저장하지 못했습니다.")
		return
	}
	writeJSON(w, 201, map[string]any{"id": inquiryID, "talent_id": talentID})
}

// replyToInquiry is the same act from either side. The seller answering and the
// buyer following up are the same message on the same thread.
func (s *Server) replyToInquiry(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	id, ok := parseUUIDPath(w, r, "id")
	if !ok {
		return
	}
	body, ok := decodeInquiryBody(w, r)
	if !ok {
		return
	}
	tx, err := s.DB.BeginTx(r.Context(), pgx.TxOptions{})
	if err != nil {
		writeError(w, 500, "transaction_failed", "답변을 시작하지 못했습니다.")
		return
	}
	defer tx.Rollback(r.Context()) //nolint:errcheck
	var buyer, seller uuid.UUID
	var title string
	if err := tx.QueryRow(r.Context(), `SELECT i.buyer_id,i.seller_id,t.title FROM inquiries i JOIN talents t ON t.id=i.talent_id WHERE i.id=$1`, id).Scan(&buyer, &seller, &title); err != nil {
		writeError(w, 404, "inquiry_not_found", "문의를 찾을 수 없습니다.")
		return
	}
	if p.UserID != buyer && p.UserID != seller {
		writeError(w, 403, "inquiry_denied", "이 문의에 참여할 수 없습니다.")
		return
	}
	recipient := seller
	if p.UserID == seller {
		recipient = buyer
	}
	if err := appendInquiryMessage(r.Context(), tx, id, p.UserID, body, title, recipient); err != nil {
		writeError(w, 500, "inquiry_failed", "답변을 저장하지 못했습니다.")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, 500, "inquiry_failed", "답변을 저장하지 못했습니다.")
		return
	}
	writeJSON(w, 201, map[string]any{"inquiry_id": id})
}

func appendInquiryMessage(ctx context.Context, tx pgx.Tx, inquiryID, sender uuid.UUID, body, talentTitle string, recipient uuid.UUID) error {
	messageID := uuid.New()
	if _, err := tx.Exec(ctx, `INSERT INTO inquiry_messages(id,inquiry_id,sender_id,body) VALUES($1,$2,$3,$4)`, messageID, inquiryID, sender, body); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE inquiries SET last_message_at=now() WHERE id=$1`, inquiryID); err != nil {
		return err
	}
	return emitEvent(ctx, tx, "inquiry", inquiryID, "InquiryMessageCreated", map[string]any{
		"actor": sender, "recipient": recipient, "talent_title": talentTitle, "message_id": messageID,
	})
}

func decodeInquiryBody(w http.ResponseWriter, r *http.Request) (string, bool) {
	var in struct {
		Body string `json:"body"`
	}
	if !decodeJSON(w, r, &in) {
		return "", false
	}
	in.Body = strings.TrimSpace(in.Body)
	if len([]rune(in.Body)) < 2 || len([]rune(in.Body)) > maxInquiryBody {
		writeError(w, 400, "invalid_message", "문의 내용을 2자 이상 2,000자 이하로 입력해 주세요.")
		return "", false
	}
	return in.Body, true
}

// listMyInquiries shows both sides of the same list: threads I opened as a
// buyer and threads opened on my listings. Splitting them into two screens
// would mean a seller who also buys has to remember which one they are.
func (s *Server) listMyInquiries(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	limit := queryLimit(r, 20, 100)
	cursorAt, cursorID := requestCursor(r)
	rows, err := s.DB.Query(r.Context(), `SELECT i.id,i.talent_id,t.title,i.buyer_id,bu.display_name,i.seller_id,su.display_name,
			i.state,i.last_message_at,i.created_at,
			(SELECT m.body FROM inquiry_messages m WHERE m.inquiry_id=i.id ORDER BY m.created_at DESC LIMIT 1),
			(SELECT m.sender_id FROM inquiry_messages m WHERE m.inquiry_id=i.id ORDER BY m.created_at DESC LIMIT 1),
			(SELECT count(*) FROM inquiry_messages m WHERE m.inquiry_id=i.id AND m.sender_id<>$1 AND m.read_at IS NULL)
		FROM inquiries i JOIN talents t ON t.id=i.talent_id
		JOIN users bu ON bu.id=i.buyer_id JOIN users su ON su.id=i.seller_id
		WHERE (i.buyer_id=$1 OR i.seller_id=$1) AND ($3::timestamptz IS NULL OR (i.last_message_at,i.id) < ($3,$4))
		ORDER BY i.last_message_at DESC,i.id DESC LIMIT $2`, p.UserID, limit+1, cursorAt, cursorID)
	if err != nil {
		writeError(w, 500, "query_failed", "문의를 조회하지 못했습니다.")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id, talentID, buyer, seller uuid.UUID
		var title, buyerName, sellerName, state string
		var lastAt, created time.Time
		var preview *string
		var lastSender *uuid.UUID
		var unread int64
		if rows.Scan(&id, &talentID, &title, &buyer, &buyerName, &seller, &sellerName, &state, &lastAt, &created, &preview, &lastSender, &unread) == nil {
			items = append(items, map[string]any{"id": id, "talent_id": talentID, "talent_title": title,
				"buyer":  map[string]any{"id": buyer, "display_name": buyerName},
				"seller": map[string]any{"id": seller, "display_name": sellerName},
				"state":  state, "last_message_at": lastAt, "created_at": created,
				"last_message": preview, "last_sender_id": lastSender, "unread": unread,
				"role": inquiryRole(p.UserID, buyer)})
		}
	}
	items, next := pageResult(items, limit, func(item map[string]any) (time.Time, uuid.UUID) {
		return timeField(item, "last_message_at"), uuidField(item, "id")
	})
	writeJSON(w, 200, map[string]any{"items": items, "next_cursor": next})
}

func inquiryRole(me, buyer uuid.UUID) string {
	if me == buyer {
		return "buyer"
	}
	return "seller"
}

// listInquiryMessages returns one thread and marks the other side's messages
// read, because opening a conversation is what reading it means.
func (s *Server) listInquiryMessages(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	id, ok := parseUUIDPath(w, r, "id")
	if !ok {
		return
	}
	var buyer, seller, talentID uuid.UUID
	var title string
	if err := s.DB.QueryRow(r.Context(), `SELECT i.buyer_id,i.seller_id,i.talent_id,t.title FROM inquiries i JOIN talents t ON t.id=i.talent_id WHERE i.id=$1`, id).Scan(&buyer, &seller, &talentID, &title); err != nil {
		writeError(w, 404, "inquiry_not_found", "문의를 찾을 수 없습니다.")
		return
	}
	if p.UserID != buyer && p.UserID != seller {
		writeError(w, 403, "inquiry_denied", "이 문의를 볼 수 없습니다.")
		return
	}
	rows, err := s.DB.Query(r.Context(), `SELECT m.id,m.sender_id,u.display_name,m.body,m.created_at
		FROM inquiry_messages m JOIN users u ON u.id=m.sender_id WHERE m.inquiry_id=$1 ORDER BY m.created_at LIMIT 500`, id)
	if err != nil {
		writeError(w, 500, "query_failed", "문의 내용을 조회하지 못했습니다.")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var messageID, sender uuid.UUID
		var name, body string
		var created time.Time
		if rows.Scan(&messageID, &sender, &name, &body, &created) == nil {
			items = append(items, map[string]any{"id": messageID, "sender_id": sender, "sender_name": name, "body": body, "created_at": created})
		}
	}
	_, _ = s.DB.Exec(r.Context(), `UPDATE inquiry_messages SET read_at=now() WHERE inquiry_id=$1 AND sender_id<>$2 AND read_at IS NULL`, id, p.UserID)
	writeJSON(w, 200, map[string]any{"items": items, "talent_id": talentID, "talent_title": title,
		"role": inquiryRole(p.UserID, buyer)})
}
