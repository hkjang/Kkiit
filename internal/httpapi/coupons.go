package httpapi

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// couponTerms is the part of a coupon that decides the money, separated from
// storage so the arithmetic can be tested without a database.
type couponTerms struct {
	DiscountType      string
	DiscountValue     int64
	MinOrderAmount    int64
	MaxDiscountAmount *int64
}

// computeDiscount never returns more than the order amount, so a generous
// coupon can make an order free but can never make it negative.
func computeDiscount(terms couponTerms, amount int64) (int64, string, bool) {
	if amount <= 0 {
		return 0, "주문 금액을 확인해 주세요.", false
	}
	if amount < terms.MinOrderAmount {
		return 0, "이 쿠폰은 " + groupDigits(terms.MinOrderAmount) + "원 이상 주문에만 사용할 수 있습니다.", false
	}
	var discount int64
	switch terms.DiscountType {
	case "percent":
		if terms.DiscountValue <= 0 || terms.DiscountValue > 100 {
			return 0, "쿠폰 할인율이 올바르지 않습니다.", false
		}
		discount = amount * terms.DiscountValue / 100
	case "fixed":
		discount = terms.DiscountValue
	default:
		return 0, "지원하지 않는 쿠폰 유형입니다.", false
	}
	if terms.MaxDiscountAmount != nil && *terms.MaxDiscountAmount >= 0 && discount > *terms.MaxDiscountAmount {
		discount = *terms.MaxDiscountAmount
	}
	if discount > amount {
		discount = amount
	}
	if discount <= 0 {
		return 0, "이 주문에는 할인이 적용되지 않습니다.", false
	}
	return discount, "", true
}

// groupDigits formats an amount the way the messages show it to a buyer.
func groupDigits(value int64) string {
	text := strconv.FormatInt(value, 10)
	sign := ""
	if strings.HasPrefix(text, "-") {
		sign, text = "-", text[1:]
	}
	if len(text) <= 3 {
		return sign + text
	}
	var parts []string
	for len(text) > 3 {
		parts = append([]string{text[len(text)-3:]}, parts...)
		text = text[:len(text)-3]
	}
	return sign + text + "," + strings.Join(parts, ",")
}

type resolvedCoupon struct {
	ID       uuid.UUID
	Code     string
	Name     string
	Discount int64
}

// resolveCoupon checks the window, the switches and the usage limits inside the
// caller's transaction so two concurrent orders cannot both take the last use.
func resolveCoupon(ctx context.Context, tx pgx.Tx, code string, buyer uuid.UUID, amount int64) (resolvedCoupon, string, bool) {
	code = strings.ToUpper(strings.TrimSpace(code))
	if code == "" {
		return resolvedCoupon{}, "", true
	}
	var result resolvedCoupon
	var terms couponTerms
	var active bool
	var usageLimit *int32
	var perUser int32
	var startsAt, endsAt *time.Time
	err := tx.QueryRow(ctx, `SELECT id,code,name,discount_type,discount_value,min_order_amount,max_discount_amount,usage_limit,per_user_limit,starts_at,ends_at,active
		FROM coupons WHERE upper(code)=$1 FOR UPDATE`, code).
		Scan(&result.ID, &result.Code, &result.Name, &terms.DiscountType, &terms.DiscountValue, &terms.MinOrderAmount, &terms.MaxDiscountAmount,
			&usageLimit, &perUser, &startsAt, &endsAt, &active)
	if err != nil {
		return resolvedCoupon{}, "존재하지 않는 쿠폰 코드입니다.", false
	}
	now := time.Now()
	if !active {
		return resolvedCoupon{}, "사용이 중단된 쿠폰입니다.", false
	}
	if startsAt != nil && now.Before(*startsAt) {
		return resolvedCoupon{}, "아직 사용할 수 없는 쿠폰입니다.", false
	}
	if endsAt != nil && now.After(*endsAt) {
		return resolvedCoupon{}, "사용 기간이 지난 쿠폰입니다.", false
	}
	var total, mine int64
	if err := tx.QueryRow(ctx, `SELECT count(*),count(*) FILTER (WHERE user_id=$2) FROM coupon_redemptions WHERE coupon_id=$1`, result.ID, buyer).Scan(&total, &mine); err != nil {
		return resolvedCoupon{}, "쿠폰 사용 이력을 확인하지 못했습니다.", false
	}
	if usageLimit != nil && total >= int64(*usageLimit) {
		return resolvedCoupon{}, "쿠폰 사용 한도가 모두 소진되었습니다.", false
	}
	if perUser > 0 && mine >= int64(perUser) {
		return resolvedCoupon{}, "이 쿠폰을 사용할 수 있는 횟수를 모두 사용했습니다.", false
	}
	discount, message, ok := computeDiscount(terms, amount)
	if !ok {
		return resolvedCoupon{}, message, false
	}
	result.Discount = discount
	return result, "", true
}

// previewCoupon lets the order form show the real price before the buyer
// commits, using exactly the same rules the order will apply.
func (s *Server) previewCoupon(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	var in struct {
		Code      string     `json:"code"`
		TalentID  uuid.UUID  `json:"talent_id"`
		PackageID *uuid.UUID `json:"package_id,omitempty"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.Code) == "" {
		writeError(w, 400, "code_required", "쿠폰 코드를 입력해 주세요.")
		return
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		writeError(w, 500, "transaction_failed", "쿠폰을 확인하지 못했습니다.")
		return
	}
	defer tx.Rollback(r.Context()) //nolint:errcheck
	amount, err := orderAmount(r.Context(), tx, in.TalentID, in.PackageID)
	if err != nil {
		writeError(w, 404, "talent_not_found", "공개된 상품을 찾을 수 없습니다.")
		return
	}
	coupon, message, ok := resolveCoupon(r.Context(), tx, in.Code, p.UserID, amount)
	if !ok {
		writeError(w, 409, "coupon_not_applicable", message)
		return
	}
	writeJSON(w, 200, map[string]any{"code": coupon.Code, "name": coupon.Name, "amount": amount,
		"discount_amount": coupon.Discount, "payable_amount": amount - coupon.Discount})
}

// orderAmount is the list price of the chosen package, or the product's base
// price when no package is selected.
func orderAmount(ctx context.Context, tx pgx.Tx, talentID uuid.UUID, packageID *uuid.UUID) (int64, error) {
	var amount int64
	if err := tx.QueryRow(ctx, `SELECT t.base_price FROM talents t JOIN users u ON u.id=t.seller_id WHERE t.id=$1 AND t.status='published' AND u.status='active'`, talentID).Scan(&amount); err != nil {
		return 0, err
	}
	if packageID != nil {
		var packagePrice int64
		if err := tx.QueryRow(ctx, `SELECT price FROM talent_packages WHERE id=$1 AND talent_id=$2 AND active`, *packageID, talentID).Scan(&packagePrice); err != nil {
			return 0, err
		}
		amount = packagePrice
	}
	return amount, nil
}

type couponInput struct {
	Code              string     `json:"code"`
	Name              string     `json:"name"`
	DiscountType      string     `json:"discount_type"`
	DiscountValue     int64      `json:"discount_value"`
	MinOrderAmount    int64      `json:"min_order_amount"`
	MaxDiscountAmount *int64     `json:"max_discount_amount,omitempty"`
	UsageLimit        *int32     `json:"usage_limit,omitempty"`
	PerUserLimit      int32      `json:"per_user_limit"`
	StartsAt          *time.Time `json:"starts_at,omitempty"`
	EndsAt            *time.Time `json:"ends_at,omitempty"`
	Active            bool       `json:"active"`
}

func (in *couponInput) validate() (string, bool) {
	in.Code = strings.ToUpper(strings.TrimSpace(in.Code))
	in.Name = strings.TrimSpace(in.Name)
	if len(in.Code) < 3 || len(in.Code) > 40 || strings.ContainsAny(in.Code, " \t\n") {
		return "쿠폰 코드는 공백 없이 3자 이상 40자 이하로 입력해 주세요.", false
	}
	if in.Name == "" || len([]rune(in.Name)) > 100 {
		return "쿠폰 이름을 1자 이상 100자 이하로 입력해 주세요.", false
	}
	if in.DiscountType != "percent" && in.DiscountType != "fixed" {
		return "할인 유형은 percent 또는 fixed여야 합니다.", false
	}
	if in.DiscountType == "percent" && (in.DiscountValue <= 0 || in.DiscountValue > 100) {
		return "할인율은 1에서 100 사이여야 합니다.", false
	}
	if in.DiscountType == "fixed" && in.DiscountValue <= 0 {
		return "할인 금액은 0보다 커야 합니다.", false
	}
	if in.MinOrderAmount < 0 {
		return "최소 주문 금액을 확인해 주세요.", false
	}
	if in.MaxDiscountAmount != nil && *in.MaxDiscountAmount <= 0 {
		return "최대 할인 금액은 0보다 커야 합니다.", false
	}
	if in.UsageLimit != nil && *in.UsageLimit <= 0 {
		return "전체 사용 한도는 1 이상이어야 합니다.", false
	}
	if in.PerUserLimit < 0 {
		return "1인 사용 한도를 확인해 주세요.", false
	}
	if in.PerUserLimit == 0 {
		in.PerUserLimit = 1
	}
	if in.StartsAt != nil && in.EndsAt != nil && in.EndsAt.Before(*in.StartsAt) {
		return "종료 시점은 시작 시점보다 늦어야 합니다.", false
	}
	return "", true
}

func (s *Server) listCoupons(w http.ResponseWriter, r *http.Request) {
	rows, err := s.DB.Query(r.Context(), `SELECT c.id,c.code,c.name,c.discount_type,c.discount_value,c.min_order_amount,c.max_discount_amount,
		c.usage_limit,c.per_user_limit,c.starts_at,c.ends_at,c.active,c.created_at,
		(SELECT count(*) FROM coupon_redemptions cr WHERE cr.coupon_id=c.id),
		COALESCE((SELECT sum(cr.discount_amount) FROM coupon_redemptions cr WHERE cr.coupon_id=c.id),0)
		FROM coupons c ORDER BY c.created_at DESC LIMIT $1`, queryLimit(r, 100, 500))
	if err != nil {
		writeError(w, 500, "query_failed", "쿠폰을 조회하지 못했습니다.")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id uuid.UUID
		var code, name, discountType string
		var discountValue, minAmount, redeemedAmount int64
		var maxDiscount *int64
		var usageLimit *int32
		var perUser int32
		var startsAt, endsAt *time.Time
		var active bool
		var created time.Time
		var redemptions int64
		if rows.Scan(&id, &code, &name, &discountType, &discountValue, &minAmount, &maxDiscount, &usageLimit, &perUser,
			&startsAt, &endsAt, &active, &created, &redemptions, &redeemedAmount) != nil {
			continue
		}
		items = append(items, map[string]any{"id": id, "code": code, "name": name, "discount_type": discountType,
			"discount_value": discountValue, "min_order_amount": minAmount, "max_discount_amount": maxDiscount,
			"usage_limit": usageLimit, "per_user_limit": perUser, "starts_at": startsAt, "ends_at": endsAt,
			"active": active, "created_at": created, "redemption_count": redemptions, "redeemed_amount": redeemedAmount})
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

func (s *Server) createCoupon(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	var in couponInput
	if !decodeJSON(w, r, &in) {
		return
	}
	if message, ok := in.validate(); !ok {
		writeError(w, 400, "invalid_coupon", message)
		return
	}
	id := uuid.New()
	_, err := s.DB.Exec(r.Context(), `INSERT INTO coupons(id,code,name,discount_type,discount_value,min_order_amount,max_discount_amount,usage_limit,per_user_limit,starts_at,ends_at,active,created_by)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
		id, in.Code, in.Name, in.DiscountType, in.DiscountValue, in.MinOrderAmount, in.MaxDiscountAmount, in.UsageLimit, in.PerUserLimit,
		nullableTime(in.StartsAt), nullableTime(in.EndsAt), in.Active, p.UserID)
	if err != nil {
		writeError(w, 409, "coupon_exists", "이미 사용 중인 쿠폰 코드입니다.")
		return
	}
	s.audit(r, "coupon.create", "coupon", id.String(), nil, map[string]any{"code": in.Code, "discount_type": in.DiscountType, "discount_value": in.DiscountValue}, "success")
	writeJSON(w, 201, map[string]any{"id": id, "code": in.Code})
}

func (s *Server) updateCoupon(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDPath(w, r, "id")
	if !ok {
		return
	}
	var in couponInput
	if !decodeJSON(w, r, &in) {
		return
	}
	if message, valid := in.validate(); !valid {
		writeError(w, 400, "invalid_coupon", message)
		return
	}
	tag, err := s.DB.Exec(r.Context(), `UPDATE coupons SET code=$2,name=$3,discount_type=$4,discount_value=$5,min_order_amount=$6,max_discount_amount=$7,
		usage_limit=$8,per_user_limit=$9,starts_at=$10,ends_at=$11,active=$12 WHERE id=$1`,
		id, in.Code, in.Name, in.DiscountType, in.DiscountValue, in.MinOrderAmount, in.MaxDiscountAmount, in.UsageLimit, in.PerUserLimit,
		nullableTime(in.StartsAt), nullableTime(in.EndsAt), in.Active)
	if err != nil || tag.RowsAffected() == 0 {
		writeError(w, 404, "coupon_not_found", "쿠폰을 찾을 수 없습니다.")
		return
	}
	s.audit(r, "coupon.update", "coupon", id.String(), nil, map[string]any{"code": in.Code, "active": in.Active}, "success")
	writeJSON(w, 200, map[string]any{"ok": true})
}

// deleteCoupon stops a coupon instead of removing it, because redemptions and
// the orders that used them must stay auditable.
func (s *Server) deleteCoupon(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDPath(w, r, "id")
	if !ok {
		return
	}
	tag, err := s.DB.Exec(r.Context(), `UPDATE coupons SET active=false WHERE id=$1 AND active`, id)
	if err != nil || tag.RowsAffected() == 0 {
		writeError(w, 404, "coupon_not_found", "중단할 활성 쿠폰을 찾을 수 없습니다.")
		return
	}
	s.audit(r, "coupon.deactivate", "coupon", id.String(), nil, nil, "success")
	writeJSON(w, 200, map[string]any{"ok": true, "active": false})
}
