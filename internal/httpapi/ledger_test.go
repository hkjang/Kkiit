package httpapi

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

// An unbalanced transaction is rejected before any statement runs, so a nil
// transaction is enough to prove the guard fires first.
func TestWriteLedgerRejectsUnbalancedTransactions(t *testing.T) {
	cases := map[string][]ledgerEntry{
		"환불이 에스크로보다 큼":  {debit("Escrow", 1000), credit("Buyer Refund", 1200)},
		"차변만 있음":        {debit("Escrow", 1000)},
		"음수 금액":         {debit("Escrow", -1), credit("Buyer Refund", -1)},
		"수수료 배분이 맞지 않음": {debit("Escrow", 100000), credit("Platform Revenue", 10000), credit("Seller Payable", 80000)},
	}
	for name, entries := range cases {
		if err := writeLedger(context.Background(), nil, uuid.New(), uuid.New(), "KRW", name, entries...); !errors.Is(err, errLedgerUnbalanced) {
			t.Fatalf("%s: err=%v, 균형이 맞지 않는 거래는 거부해야 합니다", name, err)
		}
	}
}

func TestDisputeOutcomesAreComplete(t *testing.T) {
	for _, outcome := range []string{"refund_full", "refund_partial", "release_to_seller"} {
		if disputeOutcomes[outcome] == "" {
			t.Fatalf("처리 결과 %s에 표시 이름이 없습니다", outcome)
		}
	}
	if len(disputeOutcomes) != 3 {
		t.Fatalf("처리 결과 수=%d", len(disputeOutcomes))
	}
}
