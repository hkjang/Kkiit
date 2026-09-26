package httpapi

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func cap64(value int64) *int64 { return &value }

func TestComputeDiscountAppliesPercentAndCap(t *testing.T) {
	discount, _, ok := computeDiscount(couponTerms{DiscountType: "percent", DiscountValue: 15}, 200_000)
	if !ok || discount != 30_000 {
		t.Fatalf("15%% 할인=%d ok=%v", discount, ok)
	}
	capped, _, ok := computeDiscount(couponTerms{DiscountType: "percent", DiscountValue: 50, MaxDiscountAmount: cap64(20_000)}, 200_000)
	if !ok || capped != 20_000 {
		t.Fatalf("상한 적용 할인=%d ok=%v", capped, ok)
	}
}

// A fixed coupon worth more than the order makes it free, never negative.
func TestComputeDiscountNeverExceedsTheOrder(t *testing.T) {
	discount, _, ok := computeDiscount(couponTerms{DiscountType: "fixed", DiscountValue: 500_000}, 80_000)
	if !ok || discount != 80_000 {
		t.Fatalf("할인=%d ok=%v", discount, ok)
	}
}

func TestComputeDiscountRejectsUnusableCombinations(t *testing.T) {
	cases := map[string]struct {
		terms  couponTerms
		amount int64
	}{
		"최소 주문 미달":   {couponTerms{DiscountType: "fixed", DiscountValue: 5_000, MinOrderAmount: 100_000}, 50_000},
		"알 수 없는 유형":  {couponTerms{DiscountType: "bogus", DiscountValue: 10}, 50_000},
		"비율 범위 초과":   {couponTerms{DiscountType: "percent", DiscountValue: 120}, 50_000},
		"할인이 0으로 내림": {couponTerms{DiscountType: "percent", DiscountValue: 1}, 50},
		"금액 없음":      {couponTerms{DiscountType: "fixed", DiscountValue: 1_000}, 0},
	}
	for name, testCase := range cases {
		if _, message, ok := computeDiscount(testCase.terms, testCase.amount); ok {
			t.Fatalf("%s: 거부되어야 합니다", name)
		} else if message == "" {
			t.Fatalf("%s: 거부 사유가 비어 있습니다", name)
		}
	}
}

func TestCouponInputValidation(t *testing.T) {
	in := couponInput{Code: " welcome10 ", Name: "신규 가입 할인", DiscountType: "percent", DiscountValue: 10}
	if message, ok := in.validate(); !ok {
		t.Fatalf("유효한 쿠폰이 거부되었습니다: %s", message)
	}
	if in.Code != "WELCOME10" {
		t.Fatalf("코드 정규화=%q", in.Code)
	}
	if in.PerUserLimit != 1 {
		t.Fatalf("1인 한도 기본값=%d", in.PerUserLimit)
	}
	bad := map[string]couponInput{
		"코드 공백": {Code: "AB CD", Name: "이름", DiscountType: "fixed", DiscountValue: 100},
		"코드 짧음": {Code: "AB", Name: "이름", DiscountType: "fixed", DiscountValue: 100},
		"유형 오류": {Code: "ABCD", Name: "이름", DiscountType: "half", DiscountValue: 10},
		"비율 초과": {Code: "ABCD", Name: "이름", DiscountType: "percent", DiscountValue: 101},
		"금액 0":  {Code: "ABCD", Name: "이름", DiscountType: "fixed", DiscountValue: 0},
		"한도 음수": {Code: "ABCD", Name: "이름", DiscountType: "fixed", DiscountValue: 10, PerUserLimit: -1},
	}
	for name, input := range bad {
		candidate := input
		if _, ok := candidate.validate(); ok {
			t.Fatalf("%s: 거부되어야 합니다", name)
		}
	}
}

// Only a unique violation means "that code is taken". Every other database
// failure has to stay distinguishable, or an outage gets reported as a
// duplicate and nobody goes looking for the real problem.
func TestIsUniqueViolationOnlyMatchesConstraintCollisions(t *testing.T) {
	if !isUniqueViolation(&pgconn.PgError{Code: "23505", ConstraintName: "coupons_code_key"}) {
		t.Fatal("23505는 중복으로 판정되어야 합니다")
	}
	// The pool returns wrapped errors, so unwrapping has to work.
	if !isUniqueViolation(fmt.Errorf("exec update: %w", &pgconn.PgError{Code: "23505"})) {
		t.Fatal("감싸인 23505도 중복으로 판정되어야 합니다")
	}
	notDuplicates := map[string]error{
		"검사 제약 위반":    &pgconn.PgError{Code: "23514"},
		"외래키 위반":      &pgconn.PgError{Code: "23503"},
		"not null 위반": &pgconn.PgError{Code: "23502"},
		"연결 끊김":       errors.New("conn closed"),
		"nil":         nil,
	}
	for name, err := range notDuplicates {
		if isUniqueViolation(err) {
			t.Fatalf("%s: 중복으로 판정되면 안 됩니다", name)
		}
	}
}

func TestGroupDigitsFormatsMoney(t *testing.T) {
	for value, want := range map[int64]string{0: "0", 999: "999", 1000: "1,000", 1234567: "1,234,567", -45000: "-45,000"} {
		if got := groupDigits(value); got != want {
			t.Fatalf("groupDigits(%d)=%q want %q", value, got, want)
		}
	}
}
