package httpapi

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"
)

func (s *Server) adminDashboard(w http.ResponseWriter, r *http.Request) {
	var users, talents, pendingApprovals, activeOrders, highRisks, openDisputes, settlementHolds int64
	var gmv, settlementPending int64
	err := s.DB.QueryRow(r.Context(), `SELECT
		(SELECT count(*) FROM users WHERE status='active'),
		(SELECT count(*) FROM talents WHERE status='published'),
		(SELECT count(*) FROM approval_requests WHERE state='pending'),
		(SELECT count(*) FROM orders WHERE state NOT IN ('COMPLETED','CANCELLED','REFUNDED')),
		(SELECT COALESCE(sum(amount),0) FROM payments WHERE state='succeeded'),
		(SELECT COALESCE(sum(net_amount),0) FROM settlements WHERE state IN ('scheduled','confirmed')),
		(SELECT count(*) FROM risk_scores WHERE level IN ('HIGH','CRITICAL')),
		(SELECT count(*) FROM disputes WHERE state NOT IN ('resolved','closed')),
		(SELECT count(*) FROM settlements WHERE state='hold')`).Scan(&users, &talents, &pendingApprovals, &activeOrders, &gmv, &settlementPending, &highRisks, &openDisputes, &settlementHolds)
	if err != nil {
		writeError(w, 500, "query_failed", "운영 지표를 조회하지 못했습니다.")
		return
	}
	writeJSON(w, 200, map[string]any{
		"users": users, "published_talents": talents, "pending_approvals": pendingApprovals,
		"active_orders": activeOrders, "gmv": gmv, "settlement_pending": settlementPending,
		"high_risks": highRisks, "open_disputes": openDisputes, "settlement_holds": settlementHolds,
		"exception_count": pendingApprovals + highRisks + openDisputes + settlementHolds,
	})
}

func (s *Server) listAdminTalents(w http.ResponseWriter, r *http.Request) {
	rows, err := s.DB.Query(r.Context(), `SELECT t.id,t.title,t.slug,t.status,t.service_type,t.base_price,t.currency,t.delivery_days,t.quality_score,t.updated_at,u.display_name FROM talents t JOIN users u ON u.id=t.seller_id ORDER BY t.updated_at DESC LIMIT 200`)
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
	writeJSON(w, 200, map[string]any{"items": items})
}

func (s *Server) listAdminOrders(w http.ResponseWriter, r *http.Request) {
	rows, err := s.DB.Query(r.Context(), `SELECT o.id,o.order_number,o.state,o.amount,o.currency,o.due_at,o.created_at,t.title,b.display_name,seller.display_name FROM orders o JOIN talents t ON t.id=o.talent_id JOIN users b ON b.id=o.buyer_id JOIN users seller ON seller.id=o.seller_id ORDER BY o.created_at DESC LIMIT 200`)
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
		if rows.Scan(&id, &number, &state, &amount, &currency, &due, &created, &title, &buyerName, &sellerName) == nil {
			items = append(items, map[string]any{"id": id, "order_number": number, "state": state, "amount": amount, "currency": currency, "due_at": due, "created_at": created, "talent_title": title, "buyer_name": buyerName, "seller_name": sellerName})
		}
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

func (s *Server) listAdminRiskQueue(w http.ResponseWriter, r *http.Request) {
	rows, err := s.DB.Query(r.Context(), `SELECT id,resource_type,resource_id,level,score,signals,actions,model_version,calculated_at FROM risk_scores ORDER BY CASE level WHEN 'CRITICAL' THEN 1 WHEN 'HIGH' THEN 2 WHEN 'MEDIUM' THEN 3 ELSE 4 END,calculated_at DESC LIMIT 200`)
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
		if rows.Scan(&id, &resourceType, &resourceID, &level, &score, &signalsRaw, &actionsRaw, &model, &calculated) != nil {
			continue
		}
		var signals, actions any
		_ = json.Unmarshal(signalsRaw, &signals)
		_ = json.Unmarshal(actionsRaw, &actions)
		items = append(items, map[string]any{"id": id, "resource_type": resourceType, "resource_id": resourceID, "level": level, "score": score, "signals": signals, "actions": actions, "model_version": model, "calculated_at": calculated})
	}
	var openDisputes, settlementHolds int64
	_ = s.DB.QueryRow(r.Context(), `SELECT (SELECT count(*) FROM disputes WHERE state NOT IN ('resolved','closed')),(SELECT count(*) FROM settlements WHERE state='hold')`).Scan(&openDisputes, &settlementHolds)
	writeJSON(w, 200, map[string]any{"items": items, "open_disputes": openDisputes, "settlement_holds": settlementHolds})
}
