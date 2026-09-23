package httpapi

import (
	"encoding/json"
	"math"
	"net/http"
	"reflect"
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

// validateApprovalPolicy normalises the input and reports why it cannot be
// stored, so the caller can tell the administrator which field to correct
// instead of repeating one unhelpful sentence.
func validateApprovalPolicy(in *approvalPolicyInput) (string, bool) {
	in.ResourceType = strings.TrimSpace(in.ResourceType)
	in.Name = strings.TrimSpace(in.Name)
	if in.ResourceType == "" || in.Name == "" {
		return "대상 종류와 이름을 입력해 주세요.", false
	}
	if in.Priority == 0 {
		in.Priority = 100
	}
	if in.Priority < 1 {
		return "우선순위는 1 이상이어야 합니다.", false
	}
	if in.Conditions == nil {
		in.Conditions = map[string]any{}
	}
	for _, key := range []string{"min_amount", "max_amount", "quality_score_below"} {
		if raw, exists := in.Conditions[key]; exists {
			value, ok := numericValue(raw)
			if !ok || value < 0 || math.IsInf(value, 0) || math.IsNaN(value) {
				return key + "은(는) 0 이상의 숫자여야 합니다.", false
			}
		}
	}
	minimum, hasMinimum := numericValue(in.Conditions["min_amount"])
	maximum, hasMaximum := numericValue(in.Conditions["max_amount"])
	if hasMinimum && hasMaximum && minimum > maximum {
		return "min_amount는 max_amount보다 클 수 없습니다.", false
	}
	// The matcher reads these two with a []any type assertion and skips the
	// condition when it fails, which would silently widen the policy to every
	// product. Refusing the value here keeps the stored policy readable.
	for _, key := range []string{"service_types", "seller_levels"} {
		if reason, ok := validateStringListCondition(in.Conditions, key); !ok {
			return reason, false
		}
	}
	if len(in.Steps) == 0 {
		in.Steps = []map[string]any{{"role": "operator", "min_approvals": 1}}
	}
	for _, step := range in.Steps {
		role, roleOK := step["role"].(string)
		approvals, approvalsOK := numericValue(step["min_approvals"])
		if !roleOK || strings.TrimSpace(role) == "" || !approvalsOK || approvals < 1 || approvals != math.Trunc(approvals) {
			return "각 단계에는 역할과 1 이상의 정수 승인 수가 필요합니다.", false
		}
	}
	return "", true
}

// validateStringListCondition accepts an absent key and an empty list, because
// the matcher only applies the condition when it holds at least one value and
// existing policies rely on that.
func validateStringListCondition(conditions map[string]any, key string) (string, bool) {
	raw, exists := conditions[key]
	if !exists {
		return "", true
	}
	values, ok := raw.([]any)
	if !ok {
		return key + "은(는) 문자열 배열이어야 합니다.", false
	}
	for _, value := range values {
		text, ok := value.(string)
		if !ok || strings.TrimSpace(text) == "" {
			return key + "의 값은 비어 있지 않은 문자열이어야 합니다.", false
		}
	}
	return "", true
}

// storedPolicyConditions reads the conditions column as it stands now, so an
// update that leaves them alone can be told apart from one that changes them.
func (s *Server) storedPolicyConditions(r *http.Request, id uuid.UUID) (map[string]any, bool) {
	var raw []byte
	if err := s.DB.QueryRow(r.Context(), `SELECT conditions FROM approval_policies WHERE id=$1`, id).Scan(&raw); err != nil {
		return nil, false
	}
	stored := map[string]any{}
	if json.Unmarshal(raw, &stored) != nil {
		return nil, false
	}
	return stored, true
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
	if reason, ok := validateApprovalPolicy(&in); !ok {
		writeError(w, 400, "invalid_policy", "승인 정책을 확인해 주세요. "+reason)
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
	reason, ok := validateApprovalPolicy(&in)
	if !ok {
		// An administrator must always be able to switch a policy off. A row
		// stored before these condition checks existed can hold a value we now
		// refuse, and the console's enable/disable sends the row back exactly as
		// it was read, so refusing the unchanged conditions would strand it: it
		// could not be disabled, and once it has handled a request it cannot be
		// deleted either. Only saving a *different* broken condition is refused,
		// so re-check the rest of the policy with the stored value set aside and
		// then write it back untouched.
		if stored, found := s.storedPolicyConditions(r, id); found && reflect.DeepEqual(stored, in.Conditions) {
			asStored := in.Conditions
			in.Conditions = map[string]any{}
			reason, ok = validateApprovalPolicy(&in)
			in.Conditions = asStored
		}
	}
	if !ok {
		writeError(w, 400, "invalid_policy", "승인 정책을 확인해 주세요. "+reason)
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
	// Pending requests are worked oldest first, because the seller who has been
	// waiting longest for a decision is the one whose livelihood is stalled.
	// Decided requests are history, so there the recent ones lead. The list was
	// also unbounded, which was fine while only pending requests were viewed and
	// wrong the moment someone asked for every approval ever made.
	rows, err := s.DB.Query(r.Context(), `SELECT ar.id,ar.resource_type,ar.resource_id,ar.state,ar.current_step,ar.context,ar.requested_by,ar.decided_by,ar.decision_note,ar.created_at,ar.decided_at,ap.name
		FROM approval_requests ar JOIN approval_policies ap ON ap.id=ar.policy_id WHERE ar.state=$1
		ORDER BY CASE WHEN ar.state='pending' THEN ar.created_at END ASC, ar.created_at DESC LIMIT $2`, state, queryLimit(r, 100, 500))
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
		waiting := 0
		if state == "pending" {
			waiting = int(time.Since(created).Hours())
		}
		items = append(items, map[string]any{"id": id, "resource_type": typ, "resource_id": resourceID, "state": state, "current_step": step, "context": context, "requested_by": requested, "decided_by": decided, "decision_note": note, "created_at": created, "decided_at": decidedAt, "policy_name": policy, "waiting_hours": waiting})
	}
	writeJSON(w, 200, map[string]any{"items": items, "sla_hours": s.approvalSLAHours(r)})
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
			err = emitEvent(r.Context(), tx, "talent", resourceID, event, map[string]any{"approval_request_id": id, "decided_by": p.UserID, "note": in.Note})
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

// approvalSLAHours is how long a submission may wait for a decision before the
// console calls it out. A seller cannot sell while their listing sits in a
// queue, so the wait is a promise the platform makes to them.
func (s *Server) approvalSLAHours(r *http.Request) int {
	policy, err := s.settingObject(r, "sla.policy")
	if err != nil {
		return 48
	}
	return intSetting(policy, "approval_hours", 48)
}

// getAdminApprovalRequest is the listing an operator is being asked to approve.
// The request itself carries only a title, so the review screen showed a name
// and a policy and nothing else: the description, the price, the packages, who
// is selling — none of it. Approving content you cannot see is not a review,
// and this is the gate the marketplace's quality rests on.
//
// The listing is read as it stands now rather than as it was when submitted,
// because what gets published is the current version.
func (s *Server) getAdminApprovalRequest(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDPath(w, r, "id")
	if !ok {
		return
	}
	var raw []byte
	err := s.DB.QueryRow(r.Context(), `SELECT jsonb_build_object(
			'id',ar.id,'state',ar.state,'current_step',ar.current_step,'context',ar.context,
			'created_at',ar.created_at,'decided_at',ar.decided_at,'decision_note',ar.decision_note,
			'resource_type',ar.resource_type,'resource_id',ar.resource_id,
			'policy',jsonb_build_object('name',ap.name,'steps',ap.steps,'conditions',ap.conditions),
			'requested_by',(SELECT jsonb_build_object('id',ru.id,'display_name',ru.display_name) FROM users ru WHERE ru.id=ar.requested_by),
			-- Was this rejected before and sent back? That is the first thing a
			-- reviewer wants to know and it was nowhere on the screen.
			'previous',COALESCE((SELECT jsonb_agg(jsonb_build_object('at',x.decided_at,'state',x.state,'note',x.decision_note)
					ORDER BY x.decided_at DESC)
				FROM approval_requests x WHERE x.resource_type=ar.resource_type AND x.resource_id=ar.resource_id
					AND x.id<>ar.id AND x.state<>'pending'),'[]'::jsonb),
			'talent',CASE WHEN ar.resource_type='talent_publish' THEN (
				SELECT jsonb_build_object('id',t.id,'title',t.title,'summary',t.summary,'description',t.description,
					'status',t.status,'service_type',t.service_type,'base_price',t.base_price,'currency',t.currency,
					'delivery_days',t.delivery_days,'revision_count',t.revision_count,'tags',t.tags,
					'scope_included',t.scope_included,'scope_excluded',t.scope_excluded,'deliverables',t.deliverables,
					'faq',t.faq,'refund_policy',t.refund_policy,'quality_score',t.quality_score,
					'category',(SELECT c.name FROM categories c WHERE c.id=t.category_id),
					'packages',COALESCE((SELECT jsonb_agg(jsonb_build_object('name',p.name,'price',p.price,
						'delivery_days',p.delivery_days,'description',p.description) ORDER BY p.sort_order)
						FROM talent_packages p WHERE p.talent_id=t.id),'[]'::jsonb),
					'seller',jsonb_build_object('id',su.id,'display_name',su.display_name,'status',su.status,
						'level',COALESCE(sp.level,'NEW'),'rating',COALESCE(sp.rating,0),
						'published_talents',(SELECT count(*) FROM talents x WHERE x.seller_id=su.id AND x.status='published'),
						'rejected_talents',(SELECT count(*) FROM talents x WHERE x.seller_id=su.id AND x.status='rejected'),
						'reports_against',(SELECT count(*) FROM reports rp WHERE rp.resource_type='user' AND rp.resource_id=su.id)))
				FROM talents t JOIN users su ON su.id=t.seller_id
				LEFT JOIN seller_profiles sp ON sp.user_id=t.seller_id WHERE t.id=ar.resource_id)
				ELSE NULL END
		) FROM approval_requests ar JOIN approval_policies ap ON ap.id=ar.policy_id WHERE ar.id=$1`, id).Scan(&raw)
	if err == pgx.ErrNoRows {
		writeError(w, 404, "approval_not_found", "승인 요청을 찾을 수 없습니다.")
		return
	}
	if err != nil {
		s.Logger.Error("admin approval detail failed", "error", err.Error(), "request", id.String())
		writeError(w, 500, "query_failed", "승인 요청을 조회하지 못했습니다.")
		return
	}
	var payload any
	_ = json.Unmarshal(raw, &payload)
	writeJSON(w, 200, payload)
}
