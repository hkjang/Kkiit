package httpapi

import (
	"encoding/json"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type approvalPolicyInput struct {
	ResourceType string           `json:"resource_type"`
	Name         string           `json:"name"`
	Enabled      bool             `json:"enabled"`
	Priority     int              `json:"priority"`
	Conditions   map[string]any   `json:"conditions"`
	Steps        []map[string]any `json:"steps"`
}

func (s *Server) listApprovalPolicies(w http.ResponseWriter, r *http.Request) {
	rows, err := s.DB.Query(r.Context(), `SELECT id,resource_type,name,enabled,priority,conditions,steps,created_at,updated_at FROM approval_policies ORDER BY resource_type,priority`)
	if err != nil {
		writeError(w, 500, "query_failed", "승인 정책을 조회하지 못했습니다.")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id uuid.UUID
		var typ, name string
		var enabled bool
		var priority int
		var conditions, steps []byte
		var created, updated time.Time
		if rows.Scan(&id, &typ, &name, &enabled, &priority, &conditions, &steps, &created, &updated) != nil {
			continue
		}
		var c, st any
		_ = json.Unmarshal(conditions, &c)
		_ = json.Unmarshal(steps, &st)
		items = append(items, map[string]any{"id": id, "resource_type": typ, "name": name, "enabled": enabled, "priority": priority, "conditions": c, "steps": st, "created_at": created, "updated_at": updated})
	}
	writeJSON(w, 200, map[string]any{"items": items, "bypass_when_no_policy": true})
}

func validateApprovalPolicy(in *approvalPolicyInput) bool {
	in.ResourceType = strings.TrimSpace(in.ResourceType)
	in.Name = strings.TrimSpace(in.Name)
	if in.ResourceType == "" || in.Name == "" {
		return false
	}
	if in.Priority == 0 {
		in.Priority = 100
	}
	if in.Priority < 1 {
		return false
	}
	if in.Conditions == nil {
		in.Conditions = map[string]any{}
	}
	for _, key := range []string{"min_amount", "max_amount", "quality_score_below"} {
		if raw, exists := in.Conditions[key]; exists {
			value, ok := numericValue(raw)
			if !ok || value < 0 || math.IsInf(value, 0) || math.IsNaN(value) {
				return false
			}
		}
	}
	minimum, hasMinimum := numericValue(in.Conditions["min_amount"])
	maximum, hasMaximum := numericValue(in.Conditions["max_amount"])
	if hasMinimum && hasMaximum && minimum > maximum {
		return false
	}
	if len(in.Steps) == 0 {
		in.Steps = []map[string]any{{"role": "operator", "min_approvals": 1}}
	}
	for _, step := range in.Steps {
		role, roleOK := step["role"].(string)
		approvals, approvalsOK := numericValue(step["min_approvals"])
		if !roleOK || strings.TrimSpace(role) == "" || !approvalsOK || approvals < 1 || approvals != math.Trunc(approvals) {
			return false
		}
	}
	return true
}

func numericValue(value any) (float64, bool) {
	switch number := value.(type) {
	case int:
		return float64(number), true
	case int64:
		return float64(number), true
	case float64:
		return number, true
	default:
		return 0, false
	}
}

func (s *Server) deleteApprovalPolicy(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDPath(w, r, "id")
	if !ok {
		return
	}
	var requestCount int
	if err := s.DB.QueryRow(r.Context(), `SELECT count(*) FROM approval_requests WHERE policy_id=$1`, id).Scan(&requestCount); err != nil {
		writeError(w, 500, "query_failed", "승인 정책의 사용 내역을 확인하지 못했습니다.")
		return
	}
	if requestCount > 0 {
		writeError(w, 409, "policy_in_use", "처리 이력이 있는 정책은 삭제할 수 없습니다. 비활성화해 주세요.")
		return
	}
	tag, err := s.DB.Exec(r.Context(), `DELETE FROM approval_policies WHERE id=$1`, id)
	if err != nil {
		writeError(w, 500, "delete_failed", "승인 정책을 삭제하지 못했습니다.")
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, 404, "not_found", "승인 정책을 찾을 수 없습니다.")
		return
	}
	s.audit(r, "approval_policy.delete", "approval_policy", id.String(), nil, nil, "success")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) createApprovalPolicy(w http.ResponseWriter, r *http.Request) {
	var in approvalPolicyInput
	if !decodeJSON(w, r, &in) {
		return
	}
	if !validateApprovalPolicy(&in) {
		writeError(w, 400, "invalid_policy", "승인 정책을 확인해 주세요.")
		return
	}
	p, _ := principalFrom(r.Context())
	id := uuid.New()
	_, err := s.DB.Exec(r.Context(), `INSERT INTO approval_policies(id,resource_type,name,enabled,priority,conditions,steps,created_by,updated_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$8)`, id, in.ResourceType, in.Name, in.Enabled, in.Priority, in.Conditions, in.Steps, p.UserID)
	if err != nil {
		writeError(w, 500, "create_failed", "승인 정책을 만들지 못했습니다.")
		return
	}
	s.audit(r, "approval_policy.create", "approval_policy", id.String(), nil, in, "success")
	writeJSON(w, 201, map[string]any{"id": id})
}

func (s *Server) updateApprovalPolicy(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDPath(w, r, "id")
	if !ok {
		return
	}
	var in approvalPolicyInput
	if !decodeJSON(w, r, &in) {
		return
	}
	if !validateApprovalPolicy(&in) {
		writeError(w, 400, "invalid_policy", "승인 정책을 확인해 주세요.")
		return
	}
	p, _ := principalFrom(r.Context())
	tag, err := s.DB.Exec(r.Context(), `UPDATE approval_policies SET resource_type=$2,name=$3,enabled=$4,priority=$5,conditions=$6,steps=$7,updated_by=$8,updated_at=now() WHERE id=$1`, id, in.ResourceType, in.Name, in.Enabled, in.Priority, in.Conditions, in.Steps, p.UserID)
	if err != nil || tag.RowsAffected() == 0 {
		writeError(w, 404, "not_found", "승인 정책을 찾을 수 없습니다.")
		return
	}
	s.audit(r, "approval_policy.update", "approval_policy", id.String(), nil, in, "success")
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) listApprovalRequests(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	if state == "" {
		state = "pending"
	}
	rows, err := s.DB.Query(r.Context(), `SELECT ar.id,ar.resource_type,ar.resource_id,ar.state,ar.current_step,ar.context,ar.requested_by,ar.decided_by,ar.decision_note,ar.created_at,ar.decided_at,ap.name FROM approval_requests ar JOIN approval_policies ap ON ap.id=ar.policy_id WHERE ar.state=$1 ORDER BY ar.created_at`, state)
	if err != nil {
		writeError(w, 500, "query_failed", "승인 대기열을 조회하지 못했습니다.")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id, resourceID uuid.UUID
		var typ, state, policy string
		var step int
		var raw []byte
		var requested, decided *uuid.UUID
		var note *string
		var created time.Time
		var decidedAt *time.Time
		if rows.Scan(&id, &typ, &resourceID, &state, &step, &raw, &requested, &decided, &note, &created, &decidedAt, &policy) != nil {
			continue
		}
		var context any
		_ = json.Unmarshal(raw, &context)
		items = append(items, map[string]any{"id": id, "resource_type": typ, "resource_id": resourceID, "state": state, "current_step": step, "context": context, "requested_by": requested, "decided_by": decided, "decision_note": note, "created_at": created, "decided_at": decidedAt, "policy_name": policy})
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

func (s *Server) decideApproval(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDPath(w, r, "id")
	if !ok {
		return
	}
	var in struct {
		Decision string `json:"decision"`
		Note     string `json:"note"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.Decision != "approved" && in.Decision != "rejected" {
		writeError(w, 400, "invalid_decision", "승인 또는 반려를 선택해 주세요.")
		return
	}
	p, _ := principalFrom(r.Context())
	tx, err := s.DB.BeginTx(r.Context(), pgx.TxOptions{})
	if err != nil {
		writeError(w, 500, "transaction_failed", "승인 처리를 시작하지 못했습니다.")
		return
	}
	defer tx.Rollback(r.Context())
	var typ string
	var resourceID uuid.UUID
	err = tx.QueryRow(r.Context(), `SELECT resource_type,resource_id FROM approval_requests WHERE id=$1 AND state='pending' FOR UPDATE`, id).Scan(&typ, &resourceID)
	if err != nil {
		writeError(w, 404, "pending_request_not_found", "처리할 승인 요청을 찾을 수 없습니다.")
		return
	}
	_, err = tx.Exec(r.Context(), `UPDATE approval_requests SET state=$2,decided_by=$3,decision_note=$4,decided_at=now() WHERE id=$1`, id, in.Decision, p.UserID, in.Note)
	if err == nil && typ == "talent_publish" {
		status := "published"
		if in.Decision == "rejected" {
			status = "rejected"
		}
		_, err = tx.Exec(r.Context(), `UPDATE talents SET status=$2,published_at=CASE WHEN $2='published' THEN now() ELSE published_at END,updated_at=now() WHERE id=$1`, resourceID, status)
		if err == nil {
			event := "TalentPublished"
			if status == "rejected" {
				event = "TalentRejected"
			}
			_, err = tx.Exec(r.Context(), `INSERT INTO domain_events(id,aggregate_type,aggregate_id,event_type,payload) VALUES($1,'talent',$2,$3,$4)`, uuid.New(), resourceID, event, map[string]any{"approval_request_id": id, "decided_by": p.UserID})
		}
	}
	if err != nil {
		writeError(w, 500, "decision_failed", "승인 결정을 저장하지 못했습니다.")
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		writeError(w, 500, "decision_failed", "승인 결정을 저장하지 못했습니다.")
		return
	}
	s.audit(r, "approval.decide", "approval_request", id.String(), nil, in, "success")
	writeJSON(w, 200, map[string]any{"ok": true, "state": in.Decision})
}
