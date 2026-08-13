package httpapi

import (
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

func (s *Server) listSettlements(w http.ResponseWriter, r *http.Request) {
	rows, err := s.DB.Query(r.Context(), `SELECT st.id,st.order_id,o.order_number,st.seller_id,u.display_name,st.gross_amount,st.platform_fee,st.pg_fee,st.tax_amount,st.net_amount,st.state,st.hold_reason,st.scheduled_at,st.settled_at,st.created_at FROM settlements st JOIN orders o ON o.id=st.order_id JOIN users u ON u.id=st.seller_id ORDER BY st.created_at DESC LIMIT 200`)
	if err != nil {
		writeError(w, 500, "query_failed", "정산을 조회하지 못했습니다.")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id, orderID, sellerID uuid.UUID
		var number, name, state string
		var gross, fee, pgFee, tax, net int64
		var hold *string
		var scheduled, settled *time.Time
		var created time.Time
		if rows.Scan(&id, &orderID, &number, &sellerID, &name, &gross, &fee, &pgFee, &tax, &net, &state, &hold, &scheduled, &settled, &created) == nil {
			items = append(items, map[string]any{"id": id, "order_id": orderID, "order_number": number, "seller_id": sellerID, "seller_name": name, "gross_amount": gross, "platform_fee": fee, "pg_fee": pgFee, "tax_amount": tax, "net_amount": net, "state": state, "hold_reason": hold, "scheduled_at": scheduled, "settled_at": settled, "created_at": created})
		}
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

func (s *Server) settlementAction(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDPath(w, r, "id")
	if !ok {
		return
	}
	var in struct {
		Action string `json:"action"`
		Reason string `json:"reason"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	in.Action = strings.ToLower(in.Action)
	var query string
	var args []any
	switch in.Action {
	case "hold":
		if strings.TrimSpace(in.Reason) == "" {
			writeError(w, 400, "hold_reason_required", "정산 보류 사유가 필요합니다.")
			return
		}
		query = `UPDATE settlements SET state='hold',hold_reason=$2 WHERE id=$1 AND state IN ('scheduled','confirmed')`
		args = []any{id, in.Reason}
	case "release":
		query = `UPDATE settlements SET state='scheduled',hold_reason=NULL WHERE id=$1 AND state='hold'`
		args = []any{id}
	case "complete":
		query = `UPDATE settlements SET state='completed',settled_at=now(),hold_reason=NULL WHERE id=$1 AND state IN ('scheduled','confirmed')`
		args = []any{id}
	default:
		writeError(w, 400, "invalid_action", "hold, release 또는 complete 작업만 가능합니다.")
		return
	}
	tag, err := s.DB.Exec(r.Context(), query, args...)
	if err != nil || tag.RowsAffected() == 0 {
		writeError(w, 409, "settlement_state_conflict", "현재 정산 상태에서 이 작업을 수행할 수 없습니다.")
		return
	}
	s.audit(r, "settlement."+in.Action, "settlement", id.String(), nil, in, "success")
	writeJSON(w, 200, map[string]any{"ok": true, "action": in.Action})
}
