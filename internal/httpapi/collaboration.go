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
	if in.Attachments == nil {
		// The column is NOT NULL and the field is optional, so a caller sending
		// only a body — the obvious minimal request — used to get an opaque 500
		// from a constraint violation. Every client we ship happens to send an
		// empty list, which is why nobody hit it.
		in.Attachments = []map[string]any{}
	}
	_, err = tx.Exec(r.Context(), `INSERT INTO messages(id,order_id,sender_id,body,attachments) VALUES($1,$2,$3,$4,$5)`, messageID, id, p.UserID, in.Body, in.Attachments)
	if err == nil {
		recipient := buyer
		if p.UserID == buyer {
			recipient = seller
		}
		err = emitEvent(r.Context(), tx, "order", id, "MessageCreated", map[string]any{"message_id": messageID, "actor": p.UserID, "recipient": recipient})
	}
	if err != nil {
		writeError(w, 500, "message_failed", "메시지를 보내지 못했습니다.")
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		writeError(w, 500, "message_failed", "메시지를 보내지 못했습니다.")
		return
	}
	s.publish(orderTopic(id), map[string]any{"type": "message.created", "order_id": id, "message_id": messageID})
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
	if err := clearDeadlines(w); err != nil {
		s.Logger.Warn("연결 타임아웃을 해제하지 못했습니다", "error", err, "path", r.URL.Path)
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return
	}
	defer conn.CloseNow() //nolint:errcheck
	conn.SetReadLimit(2048)
	updates, unsubscribe := s.subscribe(orderTopic(id))
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
		// score is the 0-100 trust score the dispatcher maintains; a review only
		// moves the raw rating it feeds on.
		_, err = tx.Exec(r.Context(), `UPDATE seller_profiles SET rating=COALESCE((SELECT avg((quality+communication+timeliness+professionalism)::numeric/4) FROM reviews WHERE seller_id=$1),0),rating_count=(SELECT count(*) FROM reviews WHERE seller_id=$1),updated_at=now() WHERE user_id=$1`, seller)
	}
	if err == nil {
		err = emitEvent(r.Context(), tx, "order", id, "ReviewCreated", map[string]any{"review_id": reviewID, "actor": p.UserID})
	}
	if err == nil && state == "ACCEPTED" {
		err = s.applyOrderTransition(r.Context(), tx, id, p.UserID, "ACCEPTED", "COMPLETED", map[string]any{"review_id": reviewID})
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

// listTalentReviews closes a gap that made the whole review flow pointless:
// buyers wrote reviews, the trust score consumed them, and nothing ever showed
// them. A star average with no writing behind it is the least persuasive thing
// on a product page, and the buyer who took the time to explain their
// experience never saw it published.
func (s *Server) listTalentReviews(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDPath(w, r, "id")
	if !ok {
		return
	}
	// A talent that is not visible to this caller does not get a review page
	// either, so an unpublished listing cannot be probed through its reviews.
	var visible bool
	if err := s.DB.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM talents t JOIN users u ON u.id=t.seller_id
		WHERE t.id=$1 AND ((t.status='published' AND u.status='active') OR t.seller_id=$2 OR $3))`,
		id, principalID(r), principalHas(r, "orders.manage")).Scan(&visible); err != nil || !visible {
		writeError(w, 404, "talent_not_found", "상품을 찾을 수 없습니다.")
		return
	}
	limit := queryLimit(r, 20, 100)
	cursorAt, cursorID := requestCursor(r)
	rows, err := s.DB.Query(r.Context(), `SELECT rv.id,rv.quality,rv.communication,rv.timeliness,rv.professionalism,rv.repurchase,rv.body,rv.created_at,u.display_name,rv.seller_reply,rv.seller_replied_at
		FROM reviews rv JOIN orders o ON o.id=rv.order_id JOIN users u ON u.id=rv.buyer_id
		WHERE o.talent_id=$1 AND ($3::timestamptz IS NULL OR (rv.created_at,rv.id) < ($3,$4))
		ORDER BY rv.created_at DESC,rv.id DESC LIMIT $2`, id, limit+1, cursorAt, cursorID)
	if err != nil {
		writeError(w, 500, "query_failed", "리뷰를 조회하지 못했습니다.")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var reviewID uuid.UUID
		var quality, communication, timeliness, professionalism int16
		var repurchase bool
		var body, name string
		var created time.Time
		var reply *string
		var repliedAt *time.Time
		if rows.Scan(&reviewID, &quality, &communication, &timeliness, &professionalism, &repurchase, &body, &created, &name, &reply, &repliedAt) == nil {
			items = append(items, map[string]any{"id": reviewID, "quality": quality, "communication": communication,
				"timeliness": timeliness, "professionalism": professionalism, "repurchase": repurchase, "body": body,
				"created_at": created, "average": float64(quality+communication+timeliness+professionalism) / 4,
				"buyer_name": maskName(name), "seller_reply": reply, "seller_replied_at": repliedAt})
		}
	}
	items, next := pageResult(items, limit, func(item map[string]any) (time.Time, uuid.UUID) {
		return timeField(item, "created_at"), uuidField(item, "id")
	})
	response := map[string]any{"items": items, "next_cursor": next}
	// The distribution is what a buyer actually reads: four fives and one one is
	// a different product from five threes with the same average.
	if cursorAt == nil {
		response["summary"] = s.reviewSummary(r, id)
	}
	writeJSON(w, 200, response)
}

func (s *Server) reviewSummary(r *http.Request, talent uuid.UUID) map[string]any {
	summary := map[string]any{"count": 0}
	var count int64
	var quality, communication, timeliness, professionalism, average *float64
	var repurchaseRate *float64
	var distribution []int64
	err := s.DB.QueryRow(r.Context(), `SELECT count(*),
			avg(rv.quality),avg(rv.communication),avg(rv.timeliness),avg(rv.professionalism),
			avg((rv.quality+rv.communication+rv.timeliness+rv.professionalism)::numeric/4),
			avg(CASE WHEN rv.repurchase THEN 1 ELSE 0 END),
			ARRAY[count(*) FILTER (WHERE round((rv.quality+rv.communication+rv.timeliness+rv.professionalism)::numeric/4)=1),
				count(*) FILTER (WHERE round((rv.quality+rv.communication+rv.timeliness+rv.professionalism)::numeric/4)=2),
				count(*) FILTER (WHERE round((rv.quality+rv.communication+rv.timeliness+rv.professionalism)::numeric/4)=3),
				count(*) FILTER (WHERE round((rv.quality+rv.communication+rv.timeliness+rv.professionalism)::numeric/4)=4),
				count(*) FILTER (WHERE round((rv.quality+rv.communication+rv.timeliness+rv.professionalism)::numeric/4)=5)]
		FROM reviews rv JOIN orders o ON o.id=rv.order_id WHERE o.talent_id=$1`, talent).
		Scan(&count, &quality, &communication, &timeliness, &professionalism, &average, &repurchaseRate, &distribution)
	if err != nil {
		return summary
	}
	return map[string]any{"count": count, "quality": quality, "communication": communication,
		"timeliness": timeliness, "professionalism": professionalism, "average": average,
		"repurchase_rate": repurchaseRate, "distribution": distribution}
}

// maskName publishes a review without publishing the reviewer. The order was
// private between two people; the review is not a reason to hand the buyer's
// full name to everyone who opens the page.
func maskName(name string) string {
	runes := []rune(strings.TrimSpace(name))
	switch len(runes) {
	case 0:
		return "익명"
	case 1:
		return string(runes)
	case 2:
		return string(runes[0]) + "*"
	default:
		return string(runes[0]) + strings.Repeat("*", len(runes)-2) + string(runes[len(runes)-1])
	}
}

// replyToReview lets the seller answer a review where it is read. The reply is
// theirs alone: it cannot change the buyer's words or the score, and it is
// published under them rather than in place of them.
func (s *Server) replyToReview(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	id, ok := parseUUIDPath(w, r, "id")
	if !ok {
		return
	}
	var in struct {
		Body string `json:"body"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	in.Body = strings.TrimSpace(in.Body)
	if len(in.Body) < 2 || len(in.Body) > 2000 {
		writeError(w, 400, "invalid_reply", "답글은 2자 이상 2,000자 이하로 입력해 주세요.")
		return
	}
	tag, err := s.DB.Exec(r.Context(), `UPDATE reviews SET seller_reply=$3,seller_replied_at=now() WHERE id=$1 AND seller_id=$2`, id, p.UserID, in.Body)
	if err != nil {
		writeError(w, 500, "reply_failed", "답글을 저장하지 못했습니다.")
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, 403, "reply_denied", "이 리뷰에 답글을 남길 수 없습니다.")
		return
	}
	s.audit(r, "review.reply", "review", id.String(), nil, map[string]any{"length": len(in.Body)}, "success")
	writeJSON(w, 200, map[string]any{"id": id, "seller_reply": in.Body})
}

// deleteReviewReply exists because a reply written in a bad moment should be
// removable by the person who wrote it.
func (s *Server) deleteReviewReply(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	id, ok := parseUUIDPath(w, r, "id")
	if !ok {
		return
	}
	tag, err := s.DB.Exec(r.Context(), `UPDATE reviews SET seller_reply=NULL,seller_replied_at=NULL WHERE id=$1 AND seller_id=$2`, id, p.UserID)
	if err != nil {
		writeError(w, 500, "reply_failed", "답글을 삭제하지 못했습니다.")
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, 403, "reply_denied", "이 리뷰의 답글을 삭제할 수 없습니다.")
		return
	}
	s.audit(r, "review.reply_delete", "review", id.String(), nil, nil, "success")
	w.WriteHeader(http.StatusNoContent)
}

// listSellerReviews is what the seller profile page needs. The page shows a
// rating and a count, and a buyer deciding whether to trust this person with a
// large job wants the sentences behind those numbers without hunting through
// each listing to find them.
func (s *Server) listSellerReviews(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDPath(w, r, "id")
	if !ok {
		return
	}
	var visible bool
	if err := s.DB.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM talents t JOIN users u ON u.id=t.seller_id
		WHERE t.seller_id=$1 AND t.status='published' AND u.status='active')`, id).Scan(&visible); err != nil || !visible {
		writeError(w, 404, "seller_not_found", "판매자를 찾을 수 없습니다.")
		return
	}
	limit := queryLimit(r, 20, 100)
	cursorAt, cursorID := requestCursor(r)
	rows, err := s.DB.Query(r.Context(), `SELECT rv.id,rv.quality,rv.communication,rv.timeliness,rv.professionalism,rv.repurchase,rv.body,rv.created_at,
			u.display_name,rv.seller_reply,rv.seller_replied_at,t.id,t.title
		FROM reviews rv JOIN orders o ON o.id=rv.order_id JOIN talents t ON t.id=o.talent_id JOIN users u ON u.id=rv.buyer_id
		WHERE rv.seller_id=$1 AND ($3::timestamptz IS NULL OR (rv.created_at,rv.id) < ($3,$4))
		ORDER BY rv.created_at DESC,rv.id DESC LIMIT $2`, id, limit+1, cursorAt, cursorID)
	if err != nil {
		writeError(w, 500, "query_failed", "후기를 조회하지 못했습니다.")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var reviewID, talentID uuid.UUID
		var quality, communication, timeliness, professionalism int16
		var repurchase bool
		var body, name, talentTitle string
		var created time.Time
		var reply *string
		var repliedAt *time.Time
		if rows.Scan(&reviewID, &quality, &communication, &timeliness, &professionalism, &repurchase, &body, &created, &name, &reply, &repliedAt, &talentID, &talentTitle) == nil {
			items = append(items, map[string]any{"id": reviewID, "quality": quality, "communication": communication,
				"timeliness": timeliness, "professionalism": professionalism, "repurchase": repurchase, "body": body,
				"created_at": created, "average": float64(quality+communication+timeliness+professionalism) / 4,
				"buyer_name": maskName(name), "seller_reply": reply, "seller_replied_at": repliedAt,
				"talent_id": talentID, "talent_title": talentTitle})
		}
	}
	items, next := pageResult(items, limit, func(item map[string]any) (time.Time, uuid.UUID) {
		return timeField(item, "created_at"), uuidField(item, "id")
	})
	writeJSON(w, 200, map[string]any{"items": items, "next_cursor": next})
}

// listMyReviews is the other half of the right of reply. A seller with twenty
// listings could answer a review only by opening each listing and reading down
// it, which means the reviews that most need an answer are the ones least
// likely to get one.
func (s *Server) listMyReviews(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	limit := queryLimit(r, 20, 100)
	cursorAt, cursorID := requestCursor(r)
	filter := ""
	if r.URL.Query().Get("unanswered") == "1" {
		filter = " AND rv.seller_reply IS NULL"
	}
	rows, err := s.DB.Query(r.Context(), `SELECT rv.id,rv.quality,rv.communication,rv.timeliness,rv.professionalism,rv.repurchase,rv.body,rv.created_at,
			u.display_name,rv.seller_reply,rv.seller_replied_at,t.id,t.title,o.order_number
		FROM reviews rv JOIN orders o ON o.id=rv.order_id JOIN talents t ON t.id=o.talent_id JOIN users u ON u.id=rv.buyer_id
		WHERE rv.seller_id=$1`+filter+` AND ($3::timestamptz IS NULL OR (rv.created_at,rv.id) < ($3,$4))
		ORDER BY rv.created_at DESC,rv.id DESC LIMIT $2`, p.UserID, limit+1, cursorAt, cursorID)
	if err != nil {
		writeError(w, 500, "query_failed", "내 리뷰를 조회하지 못했습니다.")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var reviewID, talentID uuid.UUID
		var quality, communication, timeliness, professionalism int16
		var repurchase bool
		var body, name, talentTitle, orderNumber string
		var created time.Time
		var reply *string
		var repliedAt *time.Time
		if rows.Scan(&reviewID, &quality, &communication, &timeliness, &professionalism, &repurchase, &body, &created, &name, &reply, &repliedAt, &talentID, &talentTitle, &orderNumber) == nil {
			items = append(items, map[string]any{"id": reviewID, "quality": quality, "communication": communication,
				"timeliness": timeliness, "professionalism": professionalism, "repurchase": repurchase, "body": body,
				"created_at": created, "average": float64(quality+communication+timeliness+professionalism) / 4,
				"buyer_name": maskName(name), "seller_reply": reply, "seller_replied_at": repliedAt,
				"talent_id": talentID, "talent_title": talentTitle, "order_number": orderNumber})
		}
	}
	items, next := pageResult(items, limit, func(item map[string]any) (time.Time, uuid.UUID) {
		return timeField(item, "created_at"), uuidField(item, "id")
	})
	var unanswered int64
	if err := s.DB.QueryRow(r.Context(), `SELECT count(*) FROM reviews WHERE seller_id=$1 AND seller_reply IS NULL`, p.UserID).Scan(&unanswered); err != nil {
		unanswered = 0
	}
	writeJSON(w, 200, map[string]any{"items": items, "next_cursor": next, "unanswered": unanswered})
}
