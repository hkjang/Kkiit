package worker

import "testing"

func TestScoreSellerGivesNeutralCreditWithNoHistory(t *testing.T) {
	score := scoreSeller(sellerFacts{}, defaultSellerLevels())
	if score.Score != 48 {
		t.Fatalf("이력 없는 판매자 점수=%v 구성=%v", score.Score, score.Components)
	}
	if score.Level != "NEW" {
		t.Fatalf("등급=%s", score.Level)
	}
}

func TestScoreSellerRewardsADeliveredRecord(t *testing.T) {
	fast := 2.0
	facts := sellerFacts{Completed: 24, TotalOrders: 25, OnTime: 24, DueMeasured: 24, Rating: 4.8, RatingCount: 20, ResponseHours: &fast}
	score := scoreSeller(facts, defaultSellerLevels())
	if score.Score < 90 {
		t.Fatalf("우수 판매자 점수=%v 구성=%v", score.Score, score.Components)
	}
	if score.Level != "ELITE" {
		t.Fatalf("등급=%s", score.Level)
	}
}

func TestScoreSellerPunishesDisputesAndLateDelivery(t *testing.T) {
	slow := 100.0
	facts := sellerFacts{Completed: 10, Cancelled: 6, TotalOrders: 20, OnTime: 2, DueMeasured: 10, Rating: 2.5, RatingCount: 8, Disputes: 8, ResponseHours: &slow}
	score := scoreSeller(facts, defaultSellerLevels())
	if score.Score > 45 {
		t.Fatalf("문제 있는 판매자 점수=%v 구성=%v", score.Score, score.Components)
	}
	if score.Level != "NEW" {
		t.Fatalf("등급=%s", score.Level)
	}
}

// Volume gates the level so one enthusiastic review cannot buy a badge.
func TestGradeSellerRequiresVolumeNotJustScore(t *testing.T) {
	levels := defaultSellerLevels()
	if got := gradeSeller(95, 1, levels); got != "NEW" {
		t.Fatalf("거래 1건 판매자 등급=%s", got)
	}
	if got := gradeSeller(95, 8, levels); got != "PRO" {
		t.Fatalf("거래 8건 판매자 등급=%s", got)
	}
	if got := gradeSeller(95, 25, levels); got != "ELITE" {
		t.Fatalf("거래 25건 판매자 등급=%s", got)
	}
	if got := gradeSeller(60, 25, levels); got != "RISING" {
		t.Fatalf("점수 60 판매자 등급=%s", got)
	}
}

func TestScoreTalentRewardsCompleteListings(t *testing.T) {
	thin, thinComponents := scoreTalent(talentFacts{SellerScore: 50, Description: 30, Packages: 1, PublishedAge: 200})
	full, fullComponents := scoreTalent(talentFacts{SellerScore: 50, Description: 900, Packages: 3, FAQ: 4, Orders: 12, Rating: 4.6, RatingCount: 9, PublishedAge: 5})
	if full <= thin {
		t.Fatalf("충실한 상품 점수=%v 빈약한 상품 점수=%v", full, thin)
	}
	if thinComponents["freshness"] != 0 || fullComponents["freshness"] != 10 {
		t.Fatalf("최신성 구성=%v / %v", thinComponents, fullComponents)
	}
	if fullComponents["seller"] != 20 {
		t.Fatalf("판매자 반영 구성=%v", fullComponents)
	}
}

func TestScoreTalentStaysWithinBounds(t *testing.T) {
	score, _ := scoreTalent(talentFacts{SellerScore: 100, Description: 5000, Packages: 9, FAQ: 9, Orders: 999, Rating: 5, RatingCount: 500, PublishedAge: 0})
	if score > 100 {
		t.Fatalf("상한을 넘었습니다: %v", score)
	}
	if score < 90 {
		t.Fatalf("최상 조건 점수가 너무 낮습니다: %v", score)
	}
}

func TestScoreTalentCountsPortfolioAndFavorites(t *testing.T) {
	base := talentFacts{SellerScore: 50, Description: 400, Packages: 2, FAQ: 1, PublishedAge: 10}
	plain, plainComponents := scoreTalent(base)

	withWork := base
	withWork.Portfolios = 3
	shown, shownComponents := scoreTalent(withWork)
	if shown <= plain {
		t.Fatalf("포트폴리오가 있는 상품 점수=%v 없는 상품=%v", shown, plain)
	}
	if shownComponents["completeness"]-plainComponents["completeness"] != 4 {
		t.Fatalf("포트폴리오 반영 폭=%v", shownComponents["completeness"]-plainComponents["completeness"])
	}

	wanted := base
	wanted.Favorites = 20
	saved, savedComponents := scoreTalent(wanted)
	if saved <= plain {
		t.Fatalf("찜이 많은 상품 점수=%v 없는 상품=%v", saved, plain)
	}
	if savedComponents["demand"] != 5 {
		t.Fatalf("찜 수요 반영=%v", savedComponents["demand"])
	}
}
