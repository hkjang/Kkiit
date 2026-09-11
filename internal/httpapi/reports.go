package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var reportReasons = map[string]string{
	"fraud":         "사기 의심",
	"inappropriate": "부적절한 내용",
	"spam":          "스팸·광고",
	"copyright":     "저작권 침해",
	"impersonation": "사칭",
	"other":         "기타",
}

var reportResources = map[string]bool{"talent": true, "user": true, "order": true, "message": true}

// reportActions are the enforcement outcomes an operator can pick. Each one is
// a real state change, so the queue cannot be cleared without deciding.
var reportActions = map[string]string{
	"dismiss":      "신고 기각",
	"warn":         "경고 안내",
	"hide_talent":  "상품 비공개",
	"suspend_user": "계정 정지",
}

func (s *Server) createReport(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	var in struct {
		ResourceType string           `json:"resource_type"`
		ResourceID   uuid.UUID        `json:"resource_id"`
		Reason       string           `json:"reason"`
		Details      string           `json:"details"`
		Evidence     []map[string]any `json:"evidence"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	in.Details = strings.TrimSpace(in.Details)
	if !reportResources[in.ResourceType] {
		writeError(w, 400, "invalid_resource_type", "신고할 수 있는 대상이 아닙니다.")
		return
	}
	if _, known := reportReasons[in.Reason]; !known {
		writeError(w, 400, "invalid_reason", "신고 사유를 선택해 주세요.")
		return
	}
	if len([]rune(in.Details)) > 4000 {
		writeError(w, 400, "details_too_long", "상세 내용은 4,000자 이하로 입력해 주세요.")
		return
	}
	if in.Evidence == nil {
		in.Evidence = []map[string]any{}
	}
	if len(in.Evidence) > 10 {
		writeError(w, 400, "too_many_evidence", "증빙은 10개까지 첨부할 수 있습니다.")
		return
	}
	if in.ResourceType == "user" && in.ResourceID == p.UserID {
		writeError(w, 400, "self_report_denied", "본인 계정은 신고할 수 없습니다.")
		return
	}
	if !s.reportTargetExists(r, in.ResourceType, in.ResourceID) {
		writeError(w, 404, "resource_not_found", "신고 대상을 찾을 수 없습니다.")
		return
	}
	id := uuid.New()
	_, err := s.DB.Exec(r.Context(), `INSERT INTO reports(id,reporter_id,resource_type,resource_id,reason,details,evidence) VALUES($1,$2,$3,$4,$5,$6,$7)`,
		id, p.UserID, in.ResourceType, in.ResourceID, in.Reason, in.Details, in.Evidence)
	if err != nil {
		writeError(w, 409, "report_already_open", "이미 접수되어 처리 중인 신고가 있습니다.")
		return
	}
	s.audit(r, "report.create", "report", id.String(), nil, map[string]any{"resource_type": in.ResourceType, "resource_id": in.ResourceID, "reason": in.Reason}, "success")
	writeJSON(w, 201, map[string]any{"id": id, "state": "open"})
}

func (s *Server) reportTargetExists(r *http.Request, resourceType string, id uuid.UUID) bool {
	var query string
	switch resourceType {
	case "talent":
		query = `SELECT EXISTS(SELECT 1 FROM talents WHERE id=$1)`
	case "user":
		query = `SELECT EXISTS(SELECT 1 FROM users WHERE id=$1)`
	case "order":
		query = `SELECT EXISTS(SELECT 1 FROM orders WHERE id=$1)`
	case "message":
		query = `SELECT EXISTS(SELECT 1 FROM messages WHERE id=$1)`
	default:
		return false
	}
	var exists bool
	return s.DB.QueryRow(r.Context(), query, id).Scan(&exists) == nil && exists
}

func (s *Server) listMyReports(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	rows, err := s.DB.Query(r.Context(), `SELECT id,resource_type,resource_id,reason,details,evidence,state,resolution,created_at,updated_at
		FROM reports WHERE reporter_id=$1 ORDER BY created_at DESC LIMIT $2`, p.UserID, queryLimit(r, 50, 200))
	if err != nil {
		writeError(w, 500, "query_failed", "신고 내역을 조회하지 못했습니다.")
		return
	}
	defer rows.Close()
	writeJSON(w, 200, map[string]any{"items": scanReports(rows, false), "reasons": reportReasons})
}

func (s *Server) listAdminReports(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	// Same reason as the dispute queue: the oldest unhandled report is the one
	// that has been ignored longest, so it leads.
	args := []any{queryLimit(r, 100, 500)}
	filter := ""
	if state != "" {
		args = append(args, state)
		filter = " AND r.state=$2"
	}
	rows, err := s.DB.Query(r.Context(), `SELECT r.id,r.resource_type,r.resource_id,r.reason,r.details,r.evidence,r.state,r.resolution,r.created_at,r.updated_at,
		u.display_name,
		COALESCE((SELECT t.title FROM talents t WHERE t.id=r.resource_id AND r.resource_type='talent'),
		         (SELECT tu.display_name FROM users tu WHERE tu.id=r.resource_id AND r.resource_type='user'),
		         (SELECT o.order_number FROM orders o WHERE o.id=r.resource_id AND r.resource_type='order'),''),
		(SELECT count(*) FROM reports peer WHERE peer.resource_type=r.resource_type AND peer.resource_id=r.resource_id),
		(SELECT count(*) FROM operator_notes n WHERE n.subject_type='report' AND n.subject_id=r.id)
		FROM reports r JOIN users u ON u.id=r.reporter_id
		WHERE true`+filter+`
		ORDER BY CASE r.state WHEN 'open' THEN 1 WHEN 'reviewing' THEN 2 ELSE 3 END,
			CASE WHEN r.state IN ('open','reviewing') THEN r.created_at END ASC,
			r.created_at DESC
		LIMIT $1`, args...)
	if err != nil {
		writeError(w, 500, "query_failed", "신고를 조회하지 못했습니다.")
		return
	}
	defer rows.Close()
	writeJSON(w, 200, map[string]any{"items": scanReports(rows, true), "reasons": reportReasons, "actions": reportActions})
}

func scanReports(rows pgx.Rows, withContext bool) []map[string]any {
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id, resourceID uuid.UUID
		var resourceType, reason, details, state string
		var evidenceRaw []byte
		var resolution *string
		var created, updated time.Time
		var reporterName, resourceLabel string
		var reportCount int64
		var noteCount int64
		var err error
		if withContext {
			err = rows.Scan(&id, &resourceType, &resourceID, &reason, &details, &evidenceRaw, &state, &resolution, &created, &updated, &reporterName, &resourceLabel, &reportCount, &noteCount)
		} else {
			err = rows.Scan(&id, &resourceType, &resourceID, &reason, &details, &evidenceRaw, &state, &resolution, &created, &updated)
		}
		if err != nil {
			continue
		}
		var evidence any
		_ = json.Unmarshal(evidenceRaw, &evidence)
		item := map[string]any{"id": id, "resource_type": resourceType, "resource_id": resourceID, "reason": reason,
			"reason_label": reportReasons[reason], "details": details, "evidence": evidence, "state": state,
			"resolution": resolution, "created_at": created, "updated_at": updated, "note_count": noteCount}
		if withContext {
			item["reporter_name"] = reporterName
			item["resource_label"] = resourceLabel
			item["report_count"] = reportCount
		}
		items = append(items, item)
	}
	return items
}

// resolveReport records the decision and carries it out in the same
// transaction, so a queue entry marked resolved always matches what actually
// happened to the reported resource.
func (s *Server) resolveReport(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	id, ok := parseUUIDPath(w, r, "id")
	if !ok {
		return
	}
	var in struct {
		Action string `json:"action"`
		Note   string `json:"note"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	label, known := reportActions[in.Action]
	if !known {
		writeError(w, 400, "invalid_action", "dismiss, warn, hide_talent, suspend_user 중에서 선택해 주세요.")
		return
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		writeError(w, 500, "transaction_failed", "신고 처리를 시작하지 못했습니다.")
		return
	}
	defer tx.Rollback(r.Context()) //nolint:errcheck
	var reporter, resourceID uuid.UUID
	var resourceType, state string
	err = tx.QueryRow(r.Context(), `SELECT reporter_id,resource_type,resource_id,state FROM reports WHERE id=$1 FOR UPDATE`, id).Scan(&reporter, &resourceType, &resourceID, &state)
	if err != nil {
		writeError(w, 404, "report_not_found", "신고를 찾을 수 없습니다.")
		return
	}
	if state != "open" && state != "reviewing" {
		writeError(w, 409, "report_already_resolved", "이미 처리된 신고입니다.")
		return
	}
	target, message, ok := s.enforceReport(r, tx, p, in.Action, resourceType, resourceID)
	if !ok {
		writeError(w, 409, "action_not_applicable", message)
		return
	}
	resolution := label
	if note := strings.TrimSpace(in.Note); note != "" {
		resolution = label + " · " + note
	}
	_, err = tx.Exec(r.Context(), `UPDATE reports SET state='resolved',resolution=$2,assigned_to=$3,updated_at=now() WHERE id=$1`, id, resolution, p.UserID)
	if err == nil {
		err = emitEvent(r.Context(), tx, "report", id, "ReportResolved", map[string]any{
			"action": in.Action, "action_label": label, "resource_type": resourceType, "resource_id": resourceID, "actor": p.UserID})
	}
	if err != nil {
		writeError(w, 500, "report_failed", "신고 처리를 저장하지 못했습니다.")
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		writeError(w, 500, "report_failed", "신고 처리를 저장하지 못했습니다.")
		return
	}
	if target != uuid.Nil && in.Action == "suspend_user" {
		// Sessions are revoked outside the transaction so a failure here cannot
		// undo the suspension itself.
		_, _ = s.DB.Exec(r.Context(), `UPDATE sessions SET revoked_at=now() WHERE user_id=$1 AND revoked_at IS NULL`, target)
	}
	s.audit(r, "report.resolve", "report", id.String(), map[string]any{"state": state}, map[string]any{"action": in.Action, "resolution": resolution}, "success")
	writeJSON(w, 200, map[string]any{"ok": true, "action": in.Action, "resolution": resolution})
}

// enforceReport applies the decision and returns the user it affected, if any.
func (s *Server) enforceReport(r *http.Request, tx pgx.Tx, p Principal, action, resourceType string, resourceID uuid.UUID) (uuid.UUID, string, bool) {
	switch action {
	case "dismiss", "warn":
		return uuid.Nil, "", true
	case "hide_talent":
		if resourceType != "talent" {
			return uuid.Nil, "상품 신고에만 적용할 수 있는 조치입니다.", false
		}
		var seller uuid.UUID
		var title string
		if tx.QueryRow(r.Context(), `UPDATE talents SET status='paused',updated_at=now() WHERE id=$1 AND status<>'archived' RETURNING seller_id,title`, resourceID).Scan(&seller, &title) != nil {
			return uuid.Nil, "비공개로 바꿀 상품을 찾을 수 없습니다.", false
		}
		if emitEvent(r.Context(), tx, "talent", resourceID, "TalentPaused", map[string]any{"talent_title": title, "reason": "report", "actor": p.UserID}) != nil {
			return uuid.Nil, "상품 상태를 저장하지 못했습니다.", false
		}
		return seller, "", true
	case "suspend_user":
		target := resourceID
		if resourceType == "talent" {
			if tx.QueryRow(r.Context(), `SELECT seller_id FROM talents WHERE id=$1`, resourceID).Scan(&target) != nil {
				return uuid.Nil, "정지할 계정을 찾을 수 없습니다.", false
			}
		} else if resourceType != "user" {
			return uuid.Nil, "계정 정지는 사용자 또는 상품 신고에만 적용할 수 있습니다.", false
		}
		if target == p.UserID {
			return uuid.Nil, "현재 로그인한 관리자 계정은 정지할 수 없습니다.", false
		}
		var isAdmin bool
		if tx.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM user_roles ur JOIN role_permissions rp ON rp.role_code=ur.role_code WHERE ur.user_id=$1 AND rp.permission_code='admin.access')`, target).Scan(&isAdmin) == nil && isAdmin {
			return uuid.Nil, "관리자 계정은 신고 처리로 정지할 수 없습니다.", false
		}
		tag, err := tx.Exec(r.Context(), `UPDATE users SET status='suspended',updated_at=now() WHERE id=$1 AND status='active'`, target)
		if err != nil || tag.RowsAffected() == 0 {
			return uuid.Nil, "정지할 활성 계정을 찾을 수 없습니다.", false
		}
		return target, "", true
	}
	return uuid.Nil, "지원하지 않는 조치입니다.", false
}

// setTalentStatus lets an operator take a product down or restore it without
// going through a report, which the admin console previously could not do.
func (s *Server) setTalentStatus(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	id, ok := parseUUIDPath(w, r, "id")
	if !ok {
		return
	}
	var in struct {
		Status string `json:"status"`
		Note   string `json:"note"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.Status != "published" && in.Status != "paused" && in.Status != "archived" {
		writeError(w, 400, "invalid_status", "published, paused 또는 archived만 지정할 수 있습니다.")
		return
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		writeError(w, 500, "transaction_failed", "상태 변경을 시작하지 못했습니다.")
		return
	}
	defer tx.Rollback(r.Context()) //nolint:errcheck
	var before, title string
	if err = tx.QueryRow(r.Context(), `SELECT status,title FROM talents WHERE id=$1 FOR UPDATE`, id).Scan(&before, &title); err != nil {
		writeError(w, 404, "talent_not_found", "상품을 찾을 수 없습니다.")
		return
	}
	_, err = tx.Exec(r.Context(), `UPDATE talents SET status=$2,published_at=CASE WHEN $2='published' THEN COALESCE(published_at,now()) ELSE published_at END,updated_at=now() WHERE id=$1`, id, in.Status)
	if err == nil && in.Status == "paused" {
		err = emitEvent(r.Context(), tx, "talent", id, "TalentPaused", map[string]any{"talent_title": title, "reason": strings.TrimSpace(in.Note), "actor": p.UserID})
	}
	if err == nil && in.Status == "published" && before != "published" {
		err = emitEvent(r.Context(), tx, "talent", id, "TalentPublished", map[string]any{"talent_title": title, "restored": true, "actor": p.UserID})
	}
	if err != nil {
		writeError(w, 500, "update_failed", "상품 상태를 변경하지 못했습니다.")
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		writeError(w, 500, "update_failed", "상품 상태를 변경하지 못했습니다.")
		return
	}
	s.audit(r, "talent.status", "talent", id.String(), map[string]any{"status": before}, map[string]any{"status": in.Status, "note": in.Note}, "success")
	writeJSON(w, 200, map[string]any{"ok": true, "status": in.Status})
}

// getAdminReport is the evidence behind an enforcement decision. The queue
// showed the reporter's words and the name of the thing they named, and from
// that an operator could hide a listing or suspend an account. Acting on one
// person's account of something you cannot see is not moderation.
//
// The reported thing is rendered according to what it is, because "the content"
// means different things for a listing, an account, an order and a message.
func (s *Server) getAdminReport(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDPath(w, r, "id")
	if !ok {
		return
	}
	var raw []byte
	err := s.DB.QueryRow(r.Context(), `SELECT jsonb_build_object(
			'id',rp.id,'state',rp.state,'reason',rp.reason,'details',rp.details,'evidence',rp.evidence,
			'resolution',rp.resolution,'created_at',rp.created_at,'updated_at',rp.updated_at,
			'resource_type',rp.resource_type,'resource_id',rp.resource_id,
			'reporter',jsonb_build_object('id',ru.id,'display_name',ru.display_name,'status',ru.status,
				'created_at',ru.created_at,
				-- Someone who files reports constantly is a different signal
				-- from someone reporting for the first time.
				-- A report's resolution is a sentence an operator wrote, not a
				-- structured outcome, so how many were dismissed cannot be
				-- counted from it. What is countable is how many are still
				-- open, which is the signal for someone filing faster than
				-- anyone can read.
				'reports_filed',(SELECT count(*) FROM reports x WHERE x.reporter_id=ru.id),
				'reports_open',(SELECT count(*) FROM reports x WHERE x.reporter_id=ru.id AND x.state<>'resolved')),
			-- Every report ever filed against this same thing, so a pattern is
			-- visible rather than needing to be remembered.
			'history',COALESCE((SELECT jsonb_agg(jsonb_build_object('id',x.id,'at',x.created_at,'reason',x.reason,
					'state',x.state,'resolution',x.resolution) ORDER BY x.created_at DESC)
				FROM reports x WHERE x.resource_type=rp.resource_type AND x.resource_id=rp.resource_id AND x.id<>rp.id),'[]'::jsonb),
			'subject',CASE rp.resource_type
				WHEN 'talent' THEN (SELECT jsonb_build_object('kind','talent','title',t.title,'summary',t.summary,
						'description',t.description,'status',t.status,'base_price',t.base_price,'tags',t.tags,
						'seller',jsonb_build_object('id',su.id,'display_name',su.display_name,'status',su.status))
					FROM talents t JOIN users su ON su.id=t.seller_id WHERE t.id=rp.resource_id)
				WHEN 'user' THEN (SELECT jsonb_build_object('kind','user','display_name',tu.display_name,'status',tu.status,
						'created_at',tu.created_at,'headline',COALESCE(sp.headline,''),'biography',COALESCE(sp.biography,''),
						'orders',(SELECT count(*) FROM orders o WHERE o.buyer_id=tu.id OR o.seller_id=tu.id))
					FROM users tu LEFT JOIN seller_profiles sp ON sp.user_id=tu.id WHERE tu.id=rp.resource_id)
				WHEN 'order' THEN (SELECT jsonb_build_object('kind','order','order_number',o.order_number,'state',o.state,
						'amount',o.amount,'currency',o.currency,'talent_title',t.title,
						'buyer',b.display_name,'seller',se.display_name)
					FROM orders o JOIN talents t ON t.id=o.talent_id JOIN users b ON b.id=o.buyer_id
					JOIN users se ON se.id=o.seller_id WHERE o.id=rp.resource_id)
				WHEN 'message' THEN (SELECT jsonb_build_object('kind','message','body',m.body,'at',m.created_at,
						'sender',jsonb_build_object('id',mu.id,'display_name',mu.display_name,'status',mu.status),
						'order_number',o.order_number)
					FROM messages m JOIN users mu ON mu.id=m.sender_id JOIN orders o ON o.id=m.order_id
					WHERE m.id=rp.resource_id)
				ELSE NULL END
		) FROM reports rp JOIN users ru ON ru.id=rp.reporter_id WHERE rp.id=$1`, id).Scan(&raw)
	if err == pgx.ErrNoRows {
		writeError(w, 404, "report_not_found", "신고를 찾을 수 없습니다.")
		return
	}
	if err != nil {
		s.Logger.Error("admin report detail failed", "error", err.Error(), "report", id.String())
		writeError(w, 500, "query_failed", "신고 정보를 조회하지 못했습니다.")
		return
	}
	var payload any
	_ = json.Unmarshal(raw, &payload)
	writeJSON(w, 200, payload)
}
