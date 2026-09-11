package httpapi

import (
	"errors"
	"testing"
)

func TestEstimateAICostUsesTheOperatorPriceList(t *testing.T) {
	pricing := aiPricing{InputPer1K: 0.003, OutputPer1K: 0.015}
	// 2,000 input and 1,000 output tokens: 0.006 + 0.015.
	if cost := estimateAICost(pricing, 2000, 1000); cost < 0.02099 || cost > 0.02101 {
		t.Fatalf("추정 비용=%v", cost)
	}
	// A model with no price list contributes nothing rather than guessing.
	if cost := estimateAICost(aiPricing{}, 5000, 5000); cost != 0 {
		t.Fatalf("가격 정보가 없을 때 비용=%v", cost)
	}
	// Providers that omit usage must not produce negative spend.
	if cost := estimateAICost(pricing, -10, -10); cost != 0 {
		t.Fatalf("음수 토큰 비용=%v", cost)
	}
}

func TestAIFallbackReasonNamesTheBudget(t *testing.T) {
	budget := aiFallbackReason(errAIBudgetExhausted{spent: 12, budget: 10})
	if budget == "" || budget == aiFallbackReason(errors.New("gateway down")) {
		t.Fatal("예산 소진과 게이트웨이 장애를 구분해 안내해야 합니다")
	}
	if generic := aiFallbackReason(errors.New("gateway down")); generic == "" {
		t.Fatal("일반 장애 안내가 비어 있습니다")
	}
}
