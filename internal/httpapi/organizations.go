package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var organizationRoles = map[string]string{"owner": "소유자", "admin": "관리자", "member": "구성원"}

// organizationRank orders the roles so a check can ask for "admin or above"
// without listing every role that qualifies.
var organizationRank = map[string]int{"member": 1, "admin": 2, "owner": 3}

// memberRole returns the caller's role in an organization. Platform operators
// are not members and deliberately get nothing here; they use the admin console.
func memberRole(ctx context.Context, db interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, org, user uuid.UUID) string {
	var role string
	if db.QueryRow(ctx, `SELECT role FROM organization_users WHERE organization_id=$1 AND user_id=$2`, org, user).Scan(&role) != nil {
		return ""
	}
	return role
}

func (s *Server) requireMember(w http.ResponseWriter, r *http.Request, org uuid.UUID, minimum string) (string, bool) {
	p, _ := principalFrom(r.Context())
	role := memberRole(r.Context(), s.DB, org, p.UserID)
	if role == "" {
		writeError(w, 404, "organization_not_found", "조직을 찾을 수 없습니다.")
		return "", false
	}
	if organizationRank[role] < organizationRank[minimum] {
		writeError(w, 403, "organization_role_required", organizationRoles[minimum]+" 이상만 할 수 있는 작업입니다.")
		return role, false
	}
	return role, true
}

func (s *Server) createOrganization(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	var in struct {
		Name string `json:"name"`
		Slug string `json:"slug"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	in.Slug = normalizeSlug(in.Slug)
	if in.Slug == "" {
		in.Slug = normalizeSlug(in.Name)
	}
	if len([]rune(in.Name)) < 2 || len([]rune(in.Name)) > 100 || in.Slug == "" {
		writeError(w, 400, "invalid_organization", "조직 이름을 2자 이상 100자 이하로 입력해 주세요.")
		return
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		writeError(w, 500, "transaction_failed", "조직 생성을 시작하지 못했습니다.")
		return
	}
	defer tx.Rollback(r.Context()) //nolint:errcheck
	id := uuid.New()
	_, err = tx.Exec(r.Context(), `INSERT INTO organizations(id,name,slug,created_by) VALUES($1,$2,$3,$4)`, id, in.Name, in.Slug, p.UserID)
	if err != nil {
		writeError(w, 409, "slug_taken", "이미 사용 중인 조직 주소입니다.")
		return
	}
	// The creator is the first owner; an organization without one could never
	// be administered again.
	if _, err = tx.Exec(r.Context(), `INSERT INTO organization_users(organization_id,user_id,role) VALUES($1,$2,'owner')`, id, p.UserID); err != nil {
		writeError(w, 500, "create_failed", "조직을 저장하지 못했습니다.")
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		writeError(w, 500, "create_failed", "조직을 저장하지 못했습니다.")
		return
	}
	s.audit(r, "organization.create", "organization", id.String(), nil, map[string]any{"name": in.Name, "slug": in.Slug}, "success")
	writeJSON(w, 201, map[string]any{"id": id, "name": in.Name, "slug": in.Slug, "role": "owner"})
}

func (s *Server) listMyOrganizations(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	rows, err := s.DB.Query(r.Context(), `SELECT o.id,o.name,o.slug,ou.role,o.created_at,
		(SELECT count(*) FROM organization_users m WHERE m.organization_id=o.id)
		FROM organization_users ou JOIN organizations o ON o.id=ou.organization_id
		WHERE ou.user_id=$1 ORDER BY o.created_at DESC`, p.UserID)
	if err != nil {
		writeError(w, 500, "query_failed", "조직을 조회하지 못했습니다.")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id uuid.UUID
		var name, slug, role string
		var created time.Time
		var members int64
		if rows.Scan(&id, &name, &slug, &role, &created, &members) == nil {
			items = append(items, map[string]any{"id": id, "name": name, "slug": slug, "role": role,
				"role_label": organizationRoles[role], "member_count": members, "created_at": created})
		}
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

func (s *Server) getOrganization(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDPath(w, r, "id")
	if !ok {
		return
	}
	role, ok := s.requireMember(w, r, id, "member")
	if !ok {
		return
	}
	var name, slug string
	var created time.Time
	if s.DB.QueryRow(r.Context(), `SELECT name,slug,created_at FROM organizations WHERE id=$1`, id).Scan(&name, &slug, &created) != nil {
		writeError(w, 404, "organization_not_found", "조직을 찾을 수 없습니다.")
		return
	}
	rows, err := s.DB.Query(r.Context(), `SELECT ou.user_id,u.display_name,u.username,ou.role,ou.created_at
		FROM organization_users ou JOIN users u ON u.id=ou.user_id WHERE ou.organization_id=$1
		ORDER BY CASE ou.role WHEN 'owner' THEN 1 WHEN 'admin' THEN 2 ELSE 3 END,u.display_name`, id)
	if err != nil {
		writeError(w, 500, "query_failed", "구성원을 조회하지 못했습니다.")
		return
	}
	defer rows.Close()
	members := make([]map[string]any, 0)
	for rows.Next() {
		var userID uuid.UUID
		var display, username, memberRole string
		var joined time.Time
		if rows.Scan(&userID, &display, &username, &memberRole, &joined) == nil {
			members = append(members, map[string]any{"user_id": userID, "display_name": display, "username": username,
				"role": memberRole, "role_label": organizationRoles[memberRole], "joined_at": joined})
		}
	}
	writeJSON(w, 200, map[string]any{"id": id, "name": name, "slug": slug, "created_at": created,
		"role": role, "role_label": organizationRoles[role], "members": members, "budgets": s.organizationBudgets(r, id), "spend": s.organizationSpend(r, id)})
}

func (s *Server) organizationSpend(r *http.Request, org uuid.UUID) map[string]any {
	var orders, active int64
	var gross, discount int64
	err := s.DB.QueryRow(r.Context(), `SELECT count(*),count(*) FILTER (WHERE state NOT IN ('COMPLETED','CANCELLED','REFUNDED')),
		COALESCE(sum(amount),0),COALESCE(sum(discount_amount),0) FROM orders WHERE organization_id=$1`, org).Scan(&orders, &active, &gross, &discount)
	if err != nil {
		return map[string]any{}
	}
	return map[string]any{"order_count": orders, "active_orders": active, "gross_amount": gross, "discount_amount": discount, "paid_amount": gross - discount}
}

func (s *Server) addOrganizationMember(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	id, ok := parseUUIDPath(w, r, "id")
	if !ok {
		return
	}
	if _, ok := s.requireMember(w, r, id, "admin"); !ok {
		return
	}
	var in struct {
		Account string `json:"account"`
		Role    string `json:"role"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	in.Account = strings.TrimSpace(in.Account)
	if in.Role == "" {
		in.Role = "member"
	}
	if _, known := organizationRoles[in.Role]; !known {
		writeError(w, 400, "invalid_role", "owner, admin 또는 member 중에서 선택해 주세요.")
		return
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		writeError(w, 500, "transaction_failed", "구성원 추가를 시작하지 못했습니다.")
		return
	}
	defer tx.Rollback(r.Context()) //nolint:errcheck
	var target uuid.UUID
	var name string
	if tx.QueryRow(r.Context(), `SELECT id,display_name FROM users WHERE (lower(username)=lower($1) OR lower(email)=lower($1)) AND status='active'`, in.Account).Scan(&target, &name) != nil {
		writeError(w, 404, "user_not_found", "해당 아이디 또는 이메일의 활성 계정을 찾을 수 없습니다.")
		return
	}
	tag, err := tx.Exec(r.Context(), `INSERT INTO organization_users(organization_id,user_id,role) VALUES($1,$2,$3) ON CONFLICT(organization_id,user_id) DO NOTHING`, id, target, in.Role)
	if err != nil {
		writeError(w, 500, "add_failed", "구성원을 추가하지 못했습니다.")
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, 409, "already_member", "이미 조직 구성원입니다.")
		return
	}
	var organizationName string
	_ = tx.QueryRow(r.Context(), `SELECT name FROM organizations WHERE id=$1`, id).Scan(&organizationName)
	if err = emitEvent(r.Context(), tx, "organization", id, "OrganizationMemberAdded", map[string]any{
		"member_id": target, "role": in.Role, "role_label": organizationRoles[in.Role], "organization_name": organizationName, "actor": p.UserID}); err != nil {
		writeError(w, 500, "add_failed", "구성원을 추가하지 못했습니다.")
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		writeError(w, 500, "add_failed", "구성원을 추가하지 못했습니다.")
		return
	}
	s.audit(r, "organization.member_add", "organization", id.String(), nil, map[string]any{"member": target, "role": in.Role}, "success")
	writeJSON(w, 201, map[string]any{"user_id": target, "display_name": name, "role": in.Role})
}

func (s *Server) updateOrganizationMember(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDPath(w, r, "id")
	if !ok {
		return
	}
	target, ok := parseUUIDPath(w, r, "userId")
	if !ok {
		return
	}
	if _, ok := s.requireMember(w, r, id, "admin"); !ok {
		return
	}
	var in struct {
		Role string `json:"role"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if _, known := organizationRoles[in.Role]; !known {
		writeError(w, 400, "invalid_role", "owner, admin 또는 member 중에서 선택해 주세요.")
		return
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		writeError(w, 500, "transaction_failed", "역할 변경을 시작하지 못했습니다.")
		return
	}
	defer tx.Rollback(r.Context()) //nolint:errcheck
	if in.Role != "owner" && !s.hasAnotherOwner(r.Context(), tx, id, target) {
		writeError(w, 409, "last_owner_protected", "마지막 소유자의 역할은 변경할 수 없습니다.")
		return
	}
	tag, err := tx.Exec(r.Context(), `UPDATE organization_users SET role=$3 WHERE organization_id=$1 AND user_id=$2`, id, target, in.Role)
	if err != nil || tag.RowsAffected() == 0 {
		writeError(w, 404, "member_not_found", "구성원을 찾을 수 없습니다.")
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		writeError(w, 500, "update_failed", "역할을 저장하지 못했습니다.")
		return
	}
	s.audit(r, "organization.member_update", "organization", id.String(), nil, map[string]any{"member": target, "role": in.Role}, "success")
	writeJSON(w, 200, map[string]any{"ok": true, "role": in.Role})
}

// hasAnotherOwner guards the change that would leave an organization with no
// one able to administer it.
func (s *Server) hasAnotherOwner(ctx context.Context, tx pgx.Tx, org, excluding uuid.UUID) bool {
	var count int
	if tx.QueryRow(ctx, `SELECT count(*) FROM organization_users WHERE organization_id=$1 AND role='owner' AND user_id<>$2`, org, excluding).Scan(&count) != nil {
		return false
	}
	return count > 0
}

func (s *Server) removeOrganizationMember(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDPath(w, r, "id")
	if !ok {
		return
	}
	target, ok := parseUUIDPath(w, r, "userId")
	if !ok {
		return
	}
	if _, ok := s.requireMember(w, r, id, "admin"); !ok {
		return
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		writeError(w, 500, "transaction_failed", "구성원 제거를 시작하지 못했습니다.")
		return
	}
	defer tx.Rollback(r.Context()) //nolint:errcheck
	var role string
	if tx.QueryRow(r.Context(), `SELECT role FROM organization_users WHERE organization_id=$1 AND user_id=$2`, id, target).Scan(&role) != nil {
		writeError(w, 404, "member_not_found", "구성원을 찾을 수 없습니다.")
		return
	}
	if role == "owner" && !s.hasAnotherOwner(r.Context(), tx, id, target) {
		writeError(w, 409, "last_owner_protected", "마지막 소유자는 제거할 수 없습니다.")
		return
	}
	if _, err = tx.Exec(r.Context(), `DELETE FROM organization_users WHERE organization_id=$1 AND user_id=$2`, id, target); err != nil {
		writeError(w, 500, "remove_failed", "구성원을 제거하지 못했습니다.")
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		writeError(w, 500, "remove_failed", "구성원을 제거하지 못했습니다.")
		return
	}
	s.audit(r, "organization.member_remove", "organization", id.String(), map[string]any{"role": role}, map[string]any{"member": target}, "success")
	w.WriteHeader(204)
}

func (s *Server) listOrganizationOrders(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	id, ok := parseUUIDPath(w, r, "id")
	if !ok {
		return
	}
	role, ok := s.requireMember(w, r, id, "member")
	if !ok {
		return
	}
	// A plain member sees what they ordered; admins and owners see the whole
	// organization's spend.
	limit := queryLimit(r, 50, 200)
	cursorAt, cursorID := requestCursor(r)
	rows, err := s.DB.Query(r.Context(), `SELECT o.id,o.order_number,o.state,o.amount,o.discount_amount,o.currency,o.created_at,t.title,bu.display_name,su.display_name
		FROM orders o JOIN talents t ON t.id=o.talent_id JOIN users bu ON bu.id=o.buyer_id JOIN users su ON su.id=o.seller_id
		WHERE o.organization_id=$1 AND ($4 OR o.buyer_id=$5) AND ($2::timestamptz IS NULL OR (o.created_at,o.id) < ($2,$3))
		ORDER BY o.created_at DESC,o.id DESC LIMIT $6`, id, cursorAt, cursorID, organizationRank[role] >= organizationRank["admin"], p.UserID, limit+1)
	if err != nil {
		writeError(w, 500, "query_failed", "조직 주문을 조회하지 못했습니다.")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var orderID uuid.UUID
		var number, state, currency, title, buyerName, sellerName string
		var amount, discount int64
		var created time.Time
		if rows.Scan(&orderID, &number, &state, &amount, &discount, &currency, &created, &title, &buyerName, &sellerName) == nil {
			items = append(items, map[string]any{"id": orderID, "order_number": number, "state": state, "amount": amount,
				"discount_amount": discount, "payable_amount": amount - discount, "currency": currency, "created_at": created,
				"talent_title": title, "buyer_name": buyerName, "seller_name": sellerName})
		}
	}
	items, next := pageResult(items, limit, func(item map[string]any) (time.Time, uuid.UUID) {
		return timeField(item, "created_at"), uuidField(item, "id")
	})
	writeJSON(w, 200, map[string]any{"items": items, "next_cursor": next})
}

func (s *Server) organizationBudgets(r *http.Request, org uuid.UUID) []map[string]any {
	rows, err := s.DB.Query(r.Context(), `SELECT id,scope_type,name,amount,consumed_amount,currency,starts_at,ends_at,policy
		FROM budgets WHERE organization_id=$1 ORDER BY ends_at DESC LIMIT 50`, org)
	if err != nil {
		return []map[string]any{}
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id uuid.UUID
		var scope, name, currency string
		var amount, consumed int64
		var starts, ends time.Time
		var policyRaw []byte
		if rows.Scan(&id, &scope, &name, &amount, &consumed, &currency, &starts, &ends, &policyRaw) != nil {
			continue
		}
		var policy any
		_ = json.Unmarshal(policyRaw, &policy)
		remaining := amount - consumed
		if remaining < 0 {
			remaining = 0
		}
		items = append(items, map[string]any{"id": id, "scope_type": scope, "name": name, "amount": amount,
			"consumed_amount": consumed, "remaining_amount": remaining, "currency": currency,
			"starts_at": starts, "ends_at": ends, "policy": policy, "active": !time.Now().Before(starts) && !time.Now().After(ends)})
	}
	return items
}

func (s *Server) createOrganizationBudget(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDPath(w, r, "id")
	if !ok {
		return
	}
	if _, ok := s.requireMember(w, r, id, "admin"); !ok {
		return
	}
	var in struct {
		Name     string     `json:"name"`
		Amount   int64      `json:"amount"`
		Currency string     `json:"currency"`
		StartsAt *time.Time `json:"starts_at,omitempty"`
		EndsAt   *time.Time `json:"ends_at,omitempty"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len([]rune(in.Name)) > 100 || in.Amount <= 0 {
		writeError(w, 400, "invalid_budget", "예산 이름과 0보다 큰 금액을 입력해 주세요.")
		return
	}
	if in.Currency == "" {
		in.Currency = "KRW"
	}
	starts := time.Now()
	if in.StartsAt != nil {
		starts = *in.StartsAt
	}
	ends := starts.AddDate(0, 1, 0)
	if in.EndsAt != nil {
		ends = *in.EndsAt
	}
	if !ends.After(starts) {
		writeError(w, 400, "invalid_period", "예산 종료 시점은 시작 시점보다 늦어야 합니다.")
		return
	}
	budgetID := uuid.New()
	if _, err := s.DB.Exec(r.Context(), `INSERT INTO budgets(id,organization_id,scope_type,name,amount,currency,starts_at,ends_at) VALUES($1,$2,'organization',$3,$4,$5,$6,$7)`,
		budgetID, id, in.Name, in.Amount, in.Currency, starts, ends); err != nil {
		writeError(w, 500, "create_failed", "예산을 저장하지 못했습니다.")
		return
	}
	s.audit(r, "organization.budget_create", "organization", id.String(), nil, map[string]any{"budget_id": budgetID, "amount": in.Amount}, "success")
	writeJSON(w, 201, map[string]any{"id": budgetID, "name": in.Name, "amount": in.Amount, "starts_at": starts, "ends_at": ends})
}

// reserveBudget holds the payable amount against the organization's active
// budget at order time. Reserving before the work starts is the point of a
// budget: it stops the spend rather than reporting it afterwards.
// The last return value is the budget that turned out to be exhausted. It is
// reported rather than announced here because this runs inside the order
// transaction, and that transaction is about to roll back: an event written now
// would be rolled back with it, which is exactly how this notification came to
// be advertised for a year without ever being sent.
func reserveBudget(ctx context.Context, tx pgx.Tx, org uuid.UUID, payable int64) (any, int64, string, bool, uuid.UUID) {
	if org == uuid.Nil || payable <= 0 {
		return nil, 0, "", true, uuid.Nil
	}
	var id uuid.UUID
	var name string
	var amount, consumed int64
	err := tx.QueryRow(ctx, `SELECT id,name,amount,consumed_amount FROM budgets
		WHERE organization_id=$1 AND scope_type='organization' AND now() BETWEEN starts_at AND ends_at
		ORDER BY ends_at LIMIT 1 FOR UPDATE`, org).Scan(&id, &name, &amount, &consumed)
	if err == pgx.ErrNoRows {
		// No active budget means no ceiling, not a blocked purchase.
		return nil, 0, "", true, uuid.Nil
	}
	if err != nil {
		return nil, 0, "예산을 확인하지 못했습니다.", false, uuid.Nil
	}
	if consumed+payable > amount {
		remaining := amount - consumed
		if remaining < 0 {
			remaining = 0
		}
		// The people who manage the budget are the last to find out that it
		// stopped working: a member is blocked, and nothing reaches the owner
		// until that member complains. BudgetExhausted was advertised as a
		// subscribable event and had never once been emitted.
		//
		// One announcement per budget, unless it is topped up: raising the
		// amount touches the row, so a later exhaustion is announced again
		// rather than swallowed as a duplicate.
		return nil, 0, name + " 예산의 잔액이 부족합니다. 남은 금액 " + groupDigits(remaining) + "원", false, id
	}
	if _, err := tx.Exec(ctx, `UPDATE budgets SET consumed_amount=consumed_amount+$2 WHERE id=$1`, id, payable); err != nil {
		return nil, 0, "예산을 차감하지 못했습니다.", false, uuid.Nil
	}
	return id, payable, "", true, uuid.Nil
}

// releaseBudget gives back what an order still holds, at most upTo. Passing a
// non positive upTo releases the whole remaining reservation.
func releaseBudget(ctx context.Context, tx pgx.Tx, orderID uuid.UUID, upTo int64) error {
	var budgetID *uuid.UUID
	var held int64
	if err := tx.QueryRow(ctx, `SELECT budget_id,budget_consumed FROM orders WHERE id=$1 FOR UPDATE`, orderID).Scan(&budgetID, &held); err != nil {
		return err
	}
	if budgetID == nil || held <= 0 {
		return nil
	}
	release := held
	if upTo > 0 && upTo < held {
		release = upTo
	}
	if _, err := tx.Exec(ctx, `UPDATE budgets SET consumed_amount=GREATEST(0,consumed_amount-$2) WHERE id=$1`, *budgetID, release); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `UPDATE orders SET budget_consumed=budget_consumed-$2 WHERE id=$1`, orderID, release)
	return err
}

// organizationManagerOfOrder reports whether the caller manages the
// organization an order was placed under.
func (s *Server) organizationManagerOfOrder(ctx context.Context, orderID, user uuid.UUID) bool {
	var manages bool
	err := s.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM orders o JOIN organization_users ou ON ou.organization_id=o.organization_id
		WHERE o.id=$1 AND ou.user_id=$2 AND ou.role IN ('owner','admin'))`, orderID, user).Scan(&manages)
	return err == nil && manages
}

// announceBudgetExhausted runs after the refused order has rolled back, in its
// own transaction, so the announcement survives the failure that caused it.
//
// One announcement per budget, unless it is topped up: raising the amount
// touches the row, so a later exhaustion is announced again rather than
// swallowed as a duplicate of the first.
func (s *Server) announceBudgetExhausted(ctx context.Context, budget uuid.UUID) {
	if budget == uuid.Nil {
		return
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var organizationID uuid.UUID
	var name string
	var amount, consumed int64
	var announced bool
	err = tx.QueryRow(ctx, `SELECT b.organization_id,b.name,b.amount,b.consumed_amount,
			EXISTS(SELECT 1 FROM domain_events e WHERE e.aggregate_id=b.id AND e.event_type='BudgetExhausted' AND e.created_at > b.updated_at)
		FROM budgets b WHERE b.id=$1`, budget).Scan(&organizationID, &name, &amount, &consumed, &announced)
	if err != nil || announced {
		return
	}
	remaining := amount - consumed
	if remaining < 0 {
		remaining = 0
	}
	if emitErr := emitEvent(ctx, tx, "budget", budget, "BudgetExhausted", map[string]any{
		"organization_id": organizationID, "budget_name": name, "amount": amount,
		"consumed_amount": consumed, "remaining_amount": remaining,
	}); emitErr != nil {
		return
	}
	// A blocked purchase must not fail because the announcement did; the buyer
	// has already been told why their order was refused.
	_ = tx.Commit(ctx)
}
