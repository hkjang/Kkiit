package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *Server) adminDashboard(w http.ResponseWriter, r *http.Request) {
	var users, talents, pendingApprovals, activeOrders, highRisks, openDisputes, settlementHolds, failedEvents, overdueOrders int64
	var gmv, settlementPending, breachedDisputes, staleApprovals int64
	sla := s.disputeSLAHours(r)
	approvalSLA := s.approvalSLAHours(r)
	err := s.DB.QueryRow(r.Context(), `SELECT
		(SELECT count(*) FROM users WHERE status='active'),
		(SELECT count(*) FROM talents WHERE status='published'),
		(SELECT count(*) FROM approval_requests WHERE state='pending'),
		(SELECT count(*) FROM orders WHERE state NOT IN ('COMPLETED','CANCELLED','REFUNDED')),
		(SELECT COALESCE(sum(amount),0) FROM payments WHERE state='succeeded'),
		(SELECT COALESCE(sum(net_amount),0) FROM settlements WHERE state IN ('scheduled','confirmed')),
		(SELECT count(*) FROM risk_scores WHERE level IN ('HIGH','CRITICAL')),
		(SELECT count(*) FROM disputes WHERE state NOT IN ('resolved','closed')),
		(SELECT count(*) FROM settlements WHERE state='hold'),
		(SELECT count(*) FROM domain_events WHERE status='failed')+(SELECT count(*) FROM webhook_deliveries WHERE state='failed'),
		(SELECT count(*) FROM disputes WHERE state NOT IN ('resolved','closed') AND created_at < now()-make_interval(hours => $1)),
		(SELECT count(*) FROM approval_requests WHERE state='pending' AND created_at < now()-make_interval(hours => $2)),
		(SELECT count(*) FROM orders WHERE due_at IS NOT NULL AND due_at < now() AND state NOT IN ('COMPLETED','CANCELLED','REFUNDED'))`,
		sla, approvalSLA).Scan(&users, &talents, &pendingApprovals, &activeOrders, &gmv, &settlementPending, &highRisks, &openDisputes, &settlementHolds, &failedEvents, &breachedDisputes, &staleApprovals, &overdueOrders)
	if err != nil {
		writeError(w, 500, "query_failed", "운영 지표를 조회하지 못했습니다.")
		return
	}
	writeJSON(w, 200, map[string]any{
		"users": users, "published_talents": talents, "pending_approvals": pendingApprovals,
		"active_orders": activeOrders, "gmv": gmv, "settlement_pending": settlementPending,
		"high_risks": highRisks, "open_disputes": openDisputes, "settlement_holds": settlementHolds,
		// A dispute past its promised handling time is worse than a merely open
		// one: two people's money has been frozen longer than the product said
		// it would be, so it is counted separately.
		"breached_disputes": breachedDisputes, "dispute_sla_hours": sla,
		// A submission waiting past the promised decision time is a seller who
		// cannot sell, which is a different problem from a queue that merely has
		// items in it.
		"stale_approvals": staleApprovals, "approval_sla_hours": approvalSLA,
		// An order past the date it was promised for is work waiting on someone,
		// and the queue can already be filtered to exactly these.
		"overdue_orders":  overdueOrders,
		"failed_events":   failedEvents,
		"exception_count": pendingApprovals + highRisks + openDisputes + settlementHolds + failedEvents + overdueOrders,
	})
}

// listAdminTalents pages by last change because that is the order an operator
// reviews in. The sort key is mutable, so a talent edited while someone pages
// can surface again on a later page; a duplicate is the acceptable cost of
// keeping the most recently touched work at the top.
func (s *Server) listAdminTalents(w http.ResponseWriter, r *http.Request) {
	limit := queryLimit(r, 100, 500)
	cursorAt, cursorID := requestCursor(r)
	talentArgs := []any{limit + 1, cursorAt, cursorID}
	talentFilters := ""
	bindTalent := func(value any) string {
		talentArgs = append(talentArgs, value)
		return "$" + strconv.Itoa(len(talentArgs))
	}
	if query := strings.TrimSpace(r.URL.Query().Get("q")); query != "" {
		placeholder := bindTalent(query)
		talentFilters += " AND (t.title ILIKE '%'||" + placeholder + "||'%' OR u.display_name ILIKE '%'||" + placeholder + "||'%')"
	}
	if status := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("status"))); status != "" {
		talentFilters += " AND t.status=" + bindTalent(status)
	}
	if seller, err := uuid.Parse(strings.TrimSpace(r.URL.Query().Get("seller"))); err == nil {
		talentFilters += " AND t.seller_id=" + bindTalent(seller)
	}
	rows, err := s.DB.Query(r.Context(), `SELECT t.id,t.title,t.slug,t.status,t.service_type,t.base_price,t.currency,t.delivery_days,t.quality_score,t.updated_at,u.display_name
		FROM talents t JOIN users u ON u.id=t.seller_id
		WHERE ($2::timestamptz IS NULL OR (t.updated_at,t.id) < ($2,$3))`+talentFilters+`
		ORDER BY t.updated_at DESC,t.id DESC LIMIT $1`, talentArgs...)
	if err != nil {
		writeError(w, 500, "query_failed", "상품 운영 목록을 조회하지 못했습니다.")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id uuid.UUID
		var title, slug, status, serviceType, currency, sellerName string
		var price int64
		var deliveryDays int
		var quality *float64
		var updated time.Time
		if rows.Scan(&id, &title, &slug, &status, &serviceType, &price, &currency, &deliveryDays, &quality, &updated, &sellerName) == nil {
			items = append(items, map[string]any{"id": id, "title": title, "slug": slug, "status": status, "service_type": serviceType, "base_price": price, "currency": currency, "delivery_days": deliveryDays, "quality_score": quality, "updated_at": updated, "seller_name": sellerName})
		}
	}
	items, next := pageResult(items, limit, func(item map[string]any) (time.Time, uuid.UUID) {
		return timeField(item, "updated_at"), uuidField(item, "id")
	})
	writeJSON(w, 200, map[string]any{"items": items, "next_cursor": next})
}

// listAdminOrders had no way to find one order. An operator answering "my order
// KK-… is stuck" could only page through the whole table until they saw it, and
// pivoting from a person to their orders was not possible at all.
func (s *Server) listAdminOrders(w http.ResponseWriter, r *http.Request) {
	limit := queryLimit(r, 100, 500)
	cursorAt, cursorID := requestCursor(r)
	args := []any{limit + 1, cursorAt, cursorID}
	filters := ""
	bind := func(value any) string {
		args = append(args, value)
		return "$" + strconv.Itoa(len(args))
	}
	if query := strings.TrimSpace(r.URL.Query().Get("q")); query != "" {
		// An operator pastes what the person on the other end gave them, which
		// is an order number or the name of the thing they bought.
		placeholder := bind(query)
		filters += " AND (o.order_number ILIKE " + placeholder + "||'%' OR t.title ILIKE '%'||" + placeholder + "||'%')"
	}
	if user, err := uuid.Parse(strings.TrimSpace(r.URL.Query().Get("user"))); err == nil {
		placeholder := bind(user)
		filters += " AND (o.buyer_id=" + placeholder + " OR o.seller_id=" + placeholder + ")"
	}
	if state := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("state"))); state != "" {
		if state == "OPEN" {
			filters += " AND o.state NOT IN ('COMPLETED','CANCELLED','REFUNDED')"
		} else if _, known := orderTransitions[state]; known || state == "COMPLETED" || state == "CANCELLED" || state == "REFUNDED" {
			filters += " AND o.state=" + bind(state)
		}
	}
	if r.URL.Query().Get("overdue") == "1" {
		filters += " AND o.due_at IS NOT NULL AND o.due_at < now() AND o.state NOT IN ('COMPLETED','CANCELLED','REFUNDED')"
	}
	rows, err := s.DB.Query(r.Context(), `SELECT o.id,o.order_number,o.state,o.amount,o.currency,o.due_at,o.created_at,t.title,b.display_name,seller.display_name,
			(SELECT count(*) FROM operator_notes n WHERE n.subject_type='order' AND n.subject_id=o.id)
		FROM orders o JOIN talents t ON t.id=o.talent_id JOIN users b ON b.id=o.buyer_id JOIN users seller ON seller.id=o.seller_id
		WHERE ($2::timestamptz IS NULL OR (o.created_at,o.id) < ($2,$3))`+filters+`
		ORDER BY o.created_at DESC,o.id DESC LIMIT $1`, args...)
	if err != nil {
		writeError(w, 500, "query_failed", "주문 운영 목록을 조회하지 못했습니다.")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id uuid.UUID
		var number, state, currency, title, buyerName, sellerName string
		var amount int64
		var due *time.Time
		var created time.Time
		var noteCount int64
		if rows.Scan(&id, &number, &state, &amount, &currency, &due, &created, &title, &buyerName, &sellerName, &noteCount) == nil {
			items = append(items, map[string]any{"id": id, "order_number": number, "state": state, "amount": amount, "currency": currency, "due_at": due, "created_at": created, "talent_title": title, "buyer_name": buyerName, "seller_name": sellerName, "note_count": noteCount})
		}
	}
	items, next := pageResult(items, limit, func(item map[string]any) (time.Time, uuid.UUID) {
		return timeField(item, "created_at"), uuidField(item, "id")
	})
	writeJSON(w, 200, map[string]any{"items": items, "next_cursor": next})
}

func (s *Server) listAdminRiskQueue(w http.ResponseWriter, r *http.Request) {
	// Scores are append only, so the queue shows the newest assessment per
	// resource instead of every historical row.
	// Within a severity band the worst score leads. Risk rows are rescored on
	// every scan, so their timestamp says when they were last looked at rather
	// than how long they have gone unhandled, and sorting by it alone let a
	// steady drip of new alerts push an older, worse one out of sight.
	riskArgs := []any{queryLimit(r, 200, 500)}
	levelFilter := ""
	if level := r.URL.Query().Get("level"); level != "" {
		riskArgs = append(riskArgs, level)
		levelFilter = " AND l.level=$2"
	}
	rows, err := s.DB.Query(r.Context(), `WITH latest AS (
			SELECT DISTINCT ON (resource_type, resource_id) id,resource_type,resource_id,level,score,signals,actions,model_version,calculated_at
			FROM risk_scores ORDER BY resource_type,resource_id,calculated_at DESC)
		SELECT l.id,l.resource_type,l.resource_id,l.level,l.score,l.signals,l.actions,l.model_version,l.calculated_at,
			o.order_number,o.state,o.amount,o.currency,t.title,b.display_name,se.display_name
		FROM latest l
		LEFT JOIN orders o ON l.resource_type='order' AND o.id=l.resource_id
		LEFT JOIN talents t ON t.id=o.talent_id
		LEFT JOIN users b ON b.id=o.buyer_id
		LEFT JOIN users se ON se.id=o.seller_id
		WHERE true`+levelFilter+`
		ORDER BY CASE l.level WHEN 'CRITICAL' THEN 1 WHEN 'HIGH' THEN 2 WHEN 'MEDIUM' THEN 3 ELSE 4 END,
			l.score DESC, l.calculated_at DESC LIMIT $1`, riskArgs...)
	if err != nil {
		writeError(w, 500, "query_failed", "위험 대기열을 조회하지 못했습니다.")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id, resourceID uuid.UUID
		var resourceType, level, model string
		var score float64
		var signalsRaw, actionsRaw []byte
		var calculated time.Time
		var number, orderState, currency, title, buyerName, sellerName *string
		var amount *int64
		if rows.Scan(&id, &resourceType, &resourceID, &level, &score, &signalsRaw, &actionsRaw, &model, &calculated,
			&number, &orderState, &amount, &currency, &title, &buyerName, &sellerName) != nil {
			continue
		}
		var signals, actions any
		_ = json.Unmarshal(signalsRaw, &signals)
		_ = json.Unmarshal(actionsRaw, &actions)
		item := map[string]any{"id": id, "resource_type": resourceType, "resource_id": resourceID, "level": level, "score": score, "signals": signals, "actions": actions, "model_version": model, "calculated_at": calculated}
		if number != nil {
			item["order_number"] = *number
			item["order_state"] = orderState
			item["amount"] = amount
			item["currency"] = currency
			item["talent_title"] = title
			item["buyer_name"] = buyerName
			item["seller_name"] = sellerName
		}
		items = append(items, item)
	}
	var openDisputes, settlementHolds int64
	_ = s.DB.QueryRow(r.Context(), `SELECT (SELECT count(*) FROM disputes WHERE state NOT IN ('resolved','closed')),(SELECT count(*) FROM settlements WHERE state='hold')`).Scan(&openDisputes, &settlementHolds)
	writeJSON(w, 200, map[string]any{"items": items, "open_disputes": openDisputes, "settlement_holds": settlementHolds})
}

func (s *Server) rescanRisk(w http.ResponseWriter, r *http.Request) {
	if s.Rescan == nil {
		writeError(w, 503, "dispatcher_unavailable", "이 프로세스에서는 재평가를 실행할 수 없습니다.")
		return
	}
	risk, settlements := s.Rescan(r.Context())
	s.audit(r, "risk.rescan", "risk_score", "", nil, map[string]any{"changed_scores": risk, "settlements": settlements}, "success")
	writeJSON(w, 200, map[string]any{"changed_scores": risk, "settlements_processed": settlements})
}

// listAIUsage gives an operator the cost picture the monthly budget is judged
// against. Until now the budget was a number nobody could compare to anything.
func (s *Server) listAIUsage(w http.ResponseWriter, r *http.Request) {
	setting, err := s.settingObject(r, "ai.gateway")
	if err != nil {
		writeError(w, 500, "query_failed", "AI 설정을 확인하지 못했습니다.")
		return
	}
	budget, _ := setting["monthly_budget"].(float64)
	currency, _ := setting["currency"].(string)
	if currency == "" {
		currency = "USD"
	}
	var spent float64
	var executions, inputTokens, outputTokens int64
	if err := s.DB.QueryRow(r.Context(), `SELECT COALESCE(sum(estimated_cost),0),count(*),COALESCE(sum(input_tokens),0),COALESCE(sum(output_tokens),0)
		FROM ai_usage WHERE occurred_at >= date_trunc('month', now())`).Scan(&spent, &executions, &inputTokens, &outputTokens); err != nil {
		writeError(w, 500, "query_failed", "AI 사용량을 조회하지 못했습니다.")
		return
	}
	rows, err := s.DB.Query(r.Context(), `SELECT feature,model,count(*),COALESCE(sum(input_tokens),0),COALESCE(sum(output_tokens),0),COALESCE(sum(estimated_cost),0)
		FROM ai_usage WHERE occurred_at >= date_trunc('month', now()) GROUP BY feature,model ORDER BY sum(estimated_cost) DESC LIMIT 50`)
	if err != nil {
		writeError(w, 500, "query_failed", "AI 사용량을 조회하지 못했습니다.")
		return
	}
	defer rows.Close()
	breakdown := make([]map[string]any, 0)
	for rows.Next() {
		var feature, model string
		var calls, input, output int64
		var cost float64
		if rows.Scan(&feature, &model, &calls, &input, &output, &cost) == nil {
			breakdown = append(breakdown, map[string]any{"feature": feature, "model": model, "calls": calls,
				"input_tokens": input, "output_tokens": output, "estimated_cost": cost})
		}
	}
	remaining := budget - spent
	if budget <= 0 {
		remaining = 0
	} else if remaining < 0 {
		remaining = 0
	}
	writeJSON(w, 200, map[string]any{
		"month_spent": spent, "monthly_budget": budget, "remaining": remaining, "currency": currency,
		"budget_enforced": budget > 0, "exhausted": budget > 0 && spent >= budget,
		"executions": executions, "input_tokens": inputTokens, "output_tokens": outputTokens, "breakdown": breakdown,
	})
}

// getAdminOrder is where the searchable order queue leads. Finding the order an
// operator was asked about used to end at a card with a state written on it:
// no timeline, no deliveries, no conversation, nothing to record what was
// found. The queue answered "where is it" and then stopped.
//
// It assembles the same evidence the dispute case does, because it is the same
// question asked earlier — before anyone has escalated.
func (s *Server) getAdminOrder(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDPath(w, r, "id")
	if !ok {
		return
	}
	var raw []byte
	err := s.DB.QueryRow(r.Context(), `SELECT jsonb_build_object(
			'id',o.id,'order_number',o.order_number,'state',o.state,
			'amount',o.amount,'discount_amount',o.discount_amount,'currency',o.currency,
			'requirements',o.requirements,'due_at',o.due_at,'created_at',o.created_at,'accepted_at',o.accepted_at,
			'overdue',(o.due_at IS NOT NULL AND o.due_at < now() AND o.state NOT IN ('COMPLETED','CANCELLED','REFUNDED')),
			'overdue_days',GREATEST(0,FLOOR(EXTRACT(EPOCH FROM (now()-o.due_at))/86400)),
			'talent',jsonb_build_object('id',t.id,'title',t.title),
			'buyer',jsonb_build_object('id',b.id,'display_name',b.display_name),
			'seller',jsonb_build_object('id',se.id,'display_name',se.display_name),
			'organization',(SELECT jsonb_build_object('id',og.id,'name',og.name) FROM organizations og WHERE og.id=o.organization_id),
			'money',jsonb_build_object(
				'escrow',(SELECT COALESCE(sum(CASE WHEN direction='debit' THEN -amount ELSE amount END),0)
					FROM ledger_entries WHERE order_id=o.id AND account='Escrow'),
				'paid',(SELECT COALESCE(sum(pm.amount),0) FROM payments pm WHERE pm.order_id=o.id AND pm.state='captured'),
				'refunded',(SELECT COALESCE(sum(rf.amount),0) FROM refunds rf WHERE rf.order_id=o.id),
				'settlement_state',(SELECT st.state FROM settlements st WHERE st.order_id=o.id),
				'settlement_net',(SELECT st.net_amount FROM settlements st WHERE st.order_id=o.id)),
			'disputes',COALESCE((SELECT jsonb_agg(jsonb_build_object('id',d.id,'state',d.state,'reason',d.reason,'at',d.created_at) ORDER BY d.created_at DESC)
				FROM disputes d WHERE d.order_id=o.id),'[]'::jsonb),
			'timeline',COALESCE((SELECT jsonb_agg(jsonb_build_object('at',tl.created_at,'event',tl.event_type,
					'from',tl.from_state,'to',tl.to_state,'note',tl.data->>'note') ORDER BY tl.created_at)
				FROM order_timeline tl WHERE tl.order_id=o.id),'[]'::jsonb),
			'deliveries',COALESCE((SELECT jsonb_agg(jsonb_build_object('at',dl.created_at,'type',dl.delivery_type,
					'description',dl.description) ORDER BY dl.created_at)
				FROM deliveries dl WHERE dl.order_id=o.id),'[]'::jsonb),
			'messages',COALESCE((SELECT jsonb_agg(entry ORDER BY entry->>'at')
				FROM (SELECT jsonb_build_object('at',m.created_at,'sender',mu.display_name,
						'side',CASE WHEN m.sender_id=o.buyer_id THEN 'buyer' ELSE 'seller' END,
						'body',m.body) AS entry
					FROM messages m JOIN users mu ON mu.id=m.sender_id
					WHERE m.order_id=o.id ORDER BY m.created_at DESC LIMIT 100) recent),'[]'::jsonb)
		) FROM orders o
		JOIN talents t ON t.id=o.talent_id
		JOIN users b ON b.id=o.buyer_id
		JOIN users se ON se.id=o.seller_id
		WHERE o.id=$1`, id).Scan(&raw)
	if err == pgx.ErrNoRows {
		writeError(w, 404, "order_not_found", "주문을 찾을 수 없습니다.")
		return
	}
	if err != nil {
		s.Logger.Error("admin order detail failed", "error", err.Error(), "order", id.String())
		writeError(w, 500, "query_failed", "주문 정보를 조회하지 못했습니다.")
		return
	}
	var payload any
	_ = json.Unmarshal(raw, &payload)
	writeJSON(w, 200, payload)
}
