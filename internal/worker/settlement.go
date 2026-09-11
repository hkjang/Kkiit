package worker

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// processDueSettlements acts on settlements whose scheduled date has arrived.
// It never moves money: a due settlement either goes on hold because the order
// carries a risk level the deployment refuses to pay out on, or it becomes
// confirmed, which is the state an operator releases from. Actual payout stays
// a deliberate human action.
func (w *Worker) processDueSettlements(ctx context.Context, current policy) int {
	rows, err := w.DB.Query(ctx, `SELECT s.id,s.order_id,s.seller_id,
		COALESCE((SELECT r.level FROM risk_scores r WHERE r.resource_type='order' AND r.resource_id=s.order_id ORDER BY r.calculated_at DESC LIMIT 1),'LOW')
		FROM settlements s WHERE s.state='scheduled' AND s.scheduled_at IS NOT NULL AND s.scheduled_at<=now() ORDER BY s.scheduled_at LIMIT $1`, current.SettlementBatch)
	if err != nil {
		if ctx.Err() == nil {
			w.Logger.Error("due settlement query failed", "error", err)
		}
		return 0
	}
	type dueSettlement struct {
		ID      uuid.UUID
		OrderID uuid.UUID
		Seller  uuid.UUID
		Level   string
	}
	items := make([]dueSettlement, 0, current.SettlementBatch)
	for rows.Next() {
		var item dueSettlement
		if rows.Scan(&item.ID, &item.OrderID, &item.Seller, &item.Level) == nil {
			items = append(items, item)
		}
	}
	rows.Close()
	processed := 0
	for _, item := range items {
		if ctx.Err() != nil {
			return processed
		}
		hold := containsLevel(current.Risk.HoldLevels, item.Level)
		if !hold && !current.SettlementAuto {
			continue
		}
		if err := w.applySettlementDecision(ctx, item.ID, item.OrderID, hold, item.Level); err != nil {
			w.Logger.Error("settlement decision failed", "error", err, "settlement_id", item.ID)
			continue
		}
		processed++
	}
	return processed
}

func (w *Worker) applySettlementDecision(ctx context.Context, settlementID, orderID uuid.UUID, hold bool, level string) error {
	tx, err := w.DB.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	state, reason, event := "confirmed", any(nil), "SettlementConfirmed"
	if hold {
		state, reason, event = "hold", "위험 등급 "+level, "SettlementHeld"
	}
	tag, err := tx.Exec(ctx, `UPDATE settlements SET state=$2,hold_reason=$3 WHERE id=$1 AND state='scheduled'`, settlementID, state, reason)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		// Another process already decided this settlement.
		return nil
	}
	payload := map[string]any{"order_id": orderID, "risk_level": level}
	if hold {
		payload["hold_reason"] = reason
	}
	if _, err = tx.Exec(ctx, `INSERT INTO domain_events(id,aggregate_type,aggregate_id,event_type,payload) VALUES($1,'settlement',$2,$3,$4)`,
		uuid.New(), settlementID, event, payload); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
