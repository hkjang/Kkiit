package worker

import (
	"testing"
	"time"
)

func signalCodes(assessment riskAssessment) map[string]int {
	codes := map[string]int{}
	for _, signal := range assessment.Signals {
		codes[signal.Code] = signal.Weight
	}
	return codes
}

func TestAssessOrderScoresAnOrdinaryOrderAsLow(t *testing.T) {
	now := time.Now()
	facts := orderFacts{Amount: 100_000, State: "IN_PROGRESS", BuyerAgeDays: 120, BuyerOrders24h: 1, BuyerOrders: 8, BuyerDisputes: 0, SellerCompleted: 30, SellerOrders: 35, SellerDisputes: 1, Revisions: 1}
	assessment := assessOrder(facts, defaultRiskThresholds(), now)
	if assessment.Level != "LOW" || assessment.Score != 0 {
		t.Fatalf("정상 주문 등급=%s 점수=%d 신호=%v", assessment.Level, assessment.Score, assessment.Signals)
	}
	if len(assessment.Actions) != 0 {
		t.Fatalf("정상 주문에 조치가 붙었습니다: %v", assessment.Actions)
	}
}

func TestAssessOrderFlagsNewBuyerSpendingBig(t *testing.T) {
	assessment := assessOrder(orderFacts{Amount: 900_000, State: "READY", BuyerAgeDays: 0.5, BuyerOrders24h: 6, BuyerOrders: 6, SellerCompleted: 0}, defaultRiskThresholds(), time.Now())
	codes := signalCodes(assessment)
	for _, expected := range []string{"new_buyer_high_value", "rapid_orders", "first_sale_high_value"} {
		if _, ok := codes[expected]; !ok {
			t.Fatalf("%s 신호가 없습니다: %v", expected, codes)
		}
	}
	if assessment.Score != 60 || assessment.Level != "MEDIUM" {
		t.Fatalf("점수=%d 등급=%s", assessment.Score, assessment.Level)
	}
}

func TestAssessOrderEscalatesAndRequestsSettlementHold(t *testing.T) {
	past := time.Now().Add(-48 * time.Hour)
	facts := orderFacts{Amount: 2_500_000, State: "IN_PROGRESS", DueAt: &past, BuyerAgeDays: 0.2,
		BuyerOrders24h: 9, BuyerOrders: 9, BuyerDisputes: 5, SellerCompleted: 0, SellerOrders: 4, SellerDisputes: 3, Revisions: 4}
	assessment := assessOrder(facts, defaultRiskThresholds(), time.Now())
	if assessment.Score != 100 {
		t.Fatalf("합계는 100으로 제한되어야 합니다: %d", assessment.Score)
	}
	if assessment.Level != "CRITICAL" {
		t.Fatalf("등급=%s", assessment.Level)
	}
	if len(assessment.Actions) != 1 || assessment.Actions[0] != "settlement_hold" {
		t.Fatalf("정산 보류 조치가 필요합니다: %v", assessment.Actions)
	}
}

func TestAssessOrderRespectsConfiguredThresholds(t *testing.T) {
	facts := orderFacts{Amount: 600_000, State: "READY", BuyerAgeDays: 1}
	strict := riskThresholds{High: 20, Critical: 50, Medium: 10, AutoHoldSettlemnt: true, HoldLevels: []string{"CRITICAL"}}
	assessment := assessOrder(facts, strict, time.Now())
	if assessment.Level != "HIGH" {
		t.Fatalf("낮춘 임계치에서 등급=%s 점수=%d", assessment.Level, assessment.Score)
	}
	if len(assessment.Actions) != 0 {
		t.Fatal("HOLD 대상 등급이 아니면 조치를 붙이지 않아야 합니다")
	}
	lenient := defaultRiskThresholds()
	lenient.AutoHoldSettlemnt = false
	high := assessOrder(orderFacts{Amount: 3_000_000, State: "IN_PROGRESS", BuyerAgeDays: 0.1, BuyerOrders24h: 9, SellerCompleted: 0}, lenient, time.Now())
	if len(high.Actions) != 0 {
		t.Fatalf("자동 보류를 끄면 조치가 없어야 합니다: %v", high.Actions)
	}
}

func TestOverdueOnlyCountsWhileWorkIsRunning(t *testing.T) {
	past := time.Now().Add(-time.Hour)
	delivered := assessOrder(orderFacts{Amount: 10_000, State: "DELIVERED", DueAt: &past, BuyerAgeDays: 90}, defaultRiskThresholds(), time.Now())
	if _, flagged := signalCodes(delivered)["overdue"]; flagged {
		t.Fatal("납품이 끝난 주문은 납기 초과로 보지 않습니다")
	}
	running := assessOrder(orderFacts{Amount: 10_000, State: "IN_PROGRESS", DueAt: &past, BuyerAgeDays: 90}, defaultRiskThresholds(), time.Now())
	if _, flagged := signalCodes(running)["overdue"]; !flagged {
		t.Fatal("진행 중 주문의 납기 초과를 놓쳤습니다")
	}
}
