package httpapi

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *Server) settingObjectCtx(ctx context.Context, key string) (map[string]any, error) {
	var raw []byte
	if err := s.DB.QueryRow(ctx, `SELECT value FROM system_settings WHERE key=$1`, key).Scan(&raw); err != nil {
		return nil, err
	}
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	return value, nil
}

// auditSystem records a change no person requested. The actor is left null so a
// reader can tell an automated decision from an operator's.
func (s *Server) auditSystem(ctx context.Context, action, resourceType, resourceID string, before, after any) {
	if _, err := s.DB.Exec(ctx, `INSERT INTO audit_logs(id,actor_user_id,actor_roles,action,resource_type,resource_id,before_data,after_data,request_id,result)
		VALUES($1,NULL,ARRAY['system']::text[],$2,$3,$4,$5,$6,'system','success')`,
		uuid.New(), action, resourceType, nullableString(resourceID), before, after); err != nil {
		s.Logger.Error("system audit write failed", "error", err, "action", action)
	}
}

// RunOrderMaintenance advances orders that time alone should move: unpaid ones
// that were abandoned, and delivered ones the buyer never responded to. Without
// it an abandoned order holds an organization's budget and a coupon use for
// ever, and a silent buyer keeps a seller from being paid.
func (s *Server) RunOrderMaintenance(ctx context.Context) (int, int) {
	policy, err := s.settingObjectCtx(ctx, "marketplace.policy")
	if err != nil {
		return 0, 0
	}
	return s.expireUnpaidOrders(ctx, intSetting(policy, "order_expiry_hours", 72)), s.autoAcceptDeliveries(ctx, intSetting(policy, "auto_accept_days", 0))
}

func intSetting(source map[string]any, key string, fallback int) int {
	value, ok := source[key].(float64)
	if !ok || value < 0 || value > 100000 {
		return fallback
	}
	return int(value)
}

func (s *Server) expireUnpaidOrders(ctx context.Context, hours int) int {
	if hours <= 0 {
		return 0
	}
	rows, err := s.DB.Query(ctx, `SELECT id,state FROM orders WHERE state IN ('CREATED','PAYMENT_PENDING') AND created_at < now()-make_interval(hours => $1) ORDER BY created_at LIMIT 200`, hours)
	if err != nil {
		if ctx.Err() == nil {
			s.Logger.Error("expire order query failed", "error", err)
		}
		return 0
	}
	type staleOrder struct {
		ID    uuid.UUID
		State string
	}
	stale := make([]staleOrder, 0, 200)
	for rows.Next() {
		var item staleOrder
		if rows.Scan(&item.ID, &item.State) == nil {
			stale = append(stale, item)
		}
	}
	rows.Close()
	expired := 0
	for _, item := range stale {
		if ctx.Err() != nil {
			return expired
		}
		if err := s.expireOrder(ctx, item.ID, item.State, hours); err != nil {
			s.Logger.Error("expire order failed", "error", err, "order_id", item.ID)
			continue
		}
		expired++
	}
	if expired > 0 {
		s.Logger.Info("expired unpaid orders", "count", expired, "after_hours", hours)
	}
	return expired
}

func (s *Server) expireOrder(ctx context.Context, id uuid.UUID, state string, hours int) error {
	tx, err := s.DB.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var current string
	if err := tx.QueryRow(ctx, `SELECT state FROM orders WHERE id=$1 FOR UPDATE`, id).Scan(&current); err != nil {
		return err
	}
	if current != state {
		// The buyer acted between the scan and now.
		return nil
	}
	// Freeing the redemption matters: a buyer whose order expired should not
	// lose their one allowed use of a coupon.
	if _, err := tx.Exec(ctx, `DELETE FROM coupon_redemptions WHERE order_id=$1`, id); err != nil {
		return err
	}
	if err := s.applyOrderTransition(ctx, tx, id, uuid.Nil, current, "CANCELLED", map[string]any{"reason": "expired", "after_hours": hours}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	s.auditSystem(ctx, "order.expire", "order", id.String(), map[string]any{"state": current}, map[string]any{"state": "CANCELLED", "after_hours": hours})
	return nil
}

func (s *Server) autoAcceptDeliveries(ctx context.Context, days int) int {
	if days <= 0 {
		return 0
	}
	// A disputed order must never auto accept; that is the buyer objecting.
	rows, err := s.DB.Query(ctx, `SELECT o.id,o.buyer_id,o.seller_id,o.amount,o.currency FROM orders o
		WHERE o.state='DELIVERED' AND o.updated_at < now()-make_interval(days => $1)
		AND NOT EXISTS(SELECT 1 FROM disputes d WHERE d.order_id=o.id AND d.state IN ('open','under_review'))
		ORDER BY o.updated_at LIMIT 200`, days)
	if err != nil {
		if ctx.Err() == nil {
			s.Logger.Error("auto accept query failed", "error", err)
		}
		return 0
	}
	type ripeOrder struct {
		ID       uuid.UUID
		Buyer    uuid.UUID
		Seller   uuid.UUID
		Amount   int64
		Currency string
	}
	ripe := make([]ripeOrder, 0, 200)
	for rows.Next() {
		var item ripeOrder
		if rows.Scan(&item.ID, &item.Buyer, &item.Seller, &item.Amount, &item.Currency) == nil {
			ripe = append(ripe, item)
		}
	}
	rows.Close()
	feeRate := float64(10)
	if policy, err := s.settingObjectCtx(ctx, "marketplace.policy"); err == nil {
		if value, ok := policy["platform_fee_rate"].(float64); ok && value >= 0 && value <= 100 {
			feeRate = value
		}
	}
	accepted := 0
	for _, item := range ripe {
		if ctx.Err() != nil {
			return accepted
		}
		if err := s.autoAccept(ctx, item.ID, item.Buyer, item.Seller, item.Amount, item.Currency, feeRate, days); err != nil {
			s.Logger.Error("auto accept failed", "error", err, "order_id", item.ID)
			continue
		}
		accepted++
	}
	if accepted > 0 {
		s.Logger.Info("auto accepted deliveries", "count", accepted, "after_days", days)
	}
	return accepted
}

func (s *Server) autoAccept(ctx context.Context, id, buyer, seller uuid.UUID, amount int64, currency string, feeRate float64, days int) error {
	tx, err := s.DB.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var current string
	var updated time.Time
	if err := tx.QueryRow(ctx, `SELECT state,updated_at FROM orders WHERE id=$1 FOR UPDATE`, id).Scan(&current, &updated); err != nil {
		return err
	}
	if current != "DELIVERED" {
		return nil
	}
	// The buyer stays the actor of record: this is their confirmation, made on
	// their behalf because the policy window elapsed.
	if _, _, _, err := s.acceptAndSettle(ctx, tx, id, buyer, seller, current, amount, currency, feeRate); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	s.auditSystem(ctx, "order.auto_accept", "order", id.String(), map[string]any{"state": current}, map[string]any{"state": "ACCEPTED", "after_days": days})
	return nil
}
