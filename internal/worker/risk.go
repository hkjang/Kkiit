package worker

import (
	"context"
	"strconv"
	"time"

	"github.com/google/uuid"
)

// riskModelVersion changes whenever a rule is added or reweighted so an
// operator can tell which run produced a stored score.
const riskModelVersion = "rules-1"

// orderFacts is everything the rules need about one live order. Collecting the
// facts in SQL and scoring them in Go keeps the rules readable and testable
// without a database.
type orderFacts struct {
	OrderID         uuid.UUID
	BuyerID         uuid.UUID
	SellerID        uuid.UUID
	Amount          int64
	State           string
	DueAt           *time.Time
	BuyerAgeDays    float64
	BuyerOrders24h  int
	BuyerOrders     int
	BuyerDisputes   int
	SellerCompleted int
	SellerOrders    int
	SellerDisputes  int
	Revisions       int
}

type riskSignal struct {
	Code   string `json:"code"`
	Label  string `json:"label"`
	Weight int    `json:"weight"`
	Detail string `json:"detail"`
}

type riskAssessment struct {
	Score   int
	Level   string
	Signals []riskSignal
	Actions []string
}

type riskThresholds struct {
	High              int
	Critical          int
	Medium            int
	AutoHoldSettlemnt bool
	HoldLevels        []string
}

func defaultRiskThresholds() riskThresholds {
	return riskThresholds{High: 70, Critical: 90, Medium: 40, AutoHoldSettlemnt: true, HoldLevels: []string{"HIGH", "CRITICAL"}}
}

// assessOrder turns the facts into an explainable score. Every rule contributes
// a named signal so the operator queue can say why an order surfaced instead of
// showing a bare number.
func assessOrder(facts orderFacts, thresholds riskThresholds, now time.Time) riskAssessment {
	result := riskAssessment{Signals: []riskSignal{}, Actions: []string{}}
	add := func(code, label string, weight int, detail string) {
		result.Signals = append(result.Signals, riskSignal{Code: code, Label: label, Weight: weight, Detail: detail})
		result.Score += weight
	}
	if facts.BuyerAgeDays < 3 && facts.Amount >= 500_000 {
		add("new_buyer_high_value", "신규 구매자 고액 주문", 25, "가입 3일 이내 계정의 고액 주문")
	}
	if facts.BuyerOrders24h >= 5 {
		add("rapid_orders", "단시간 다량 주문", 20, "24시간 내 주문 "+strconv.Itoa(facts.BuyerOrders24h)+"건")
	}
	if facts.BuyerOrders >= 3 && ratio(facts.BuyerDisputes, facts.BuyerOrders) > 0.3 {
		add("buyer_dispute_history", "구매자 분쟁 이력", 20, "주문 "+strconv.Itoa(facts.BuyerOrders)+"건 중 분쟁 "+strconv.Itoa(facts.BuyerDisputes)+"건")
	}
	if facts.SellerOrders >= 3 && ratio(facts.SellerDisputes, facts.SellerOrders) > 0.2 {
		add("seller_dispute_history", "판매자 분쟁 이력", 25, "주문 "+strconv.Itoa(facts.SellerOrders)+"건 중 분쟁 "+strconv.Itoa(facts.SellerDisputes)+"건")
	}
	if facts.SellerCompleted == 0 && facts.Amount >= 300_000 {
		add("first_sale_high_value", "첫 거래 고액 판매", 15, "완료 이력이 없는 판매자의 고액 주문")
	}
	if facts.DueAt != nil && now.After(*facts.DueAt) && inProgressState(facts.State) {
		add("overdue", "납기 초과", 20, "납기가 지난 진행 중 주문")
	}
	if facts.Revisions >= 3 {
		add("revision_loop", "수정 반복", 15, "수정 요청 "+strconv.Itoa(facts.Revisions)+"회")
	}
	if facts.Amount >= 2_000_000 {
		add("high_value", "고액 거래", 10, "결제 금액이 200만원 이상")
	}
	if result.Score > 100 {
		result.Score = 100
	}
	switch {
	case result.Score >= thresholds.Critical:
		result.Level = "CRITICAL"
	case result.Score >= thresholds.High:
		result.Level = "HIGH"
	case result.Score >= thresholds.Medium:
		result.Level = "MEDIUM"
	default:
		result.Level = "LOW"
	}
	if thresholds.AutoHoldSettlemnt && containsLevel(thresholds.HoldLevels, result.Level) {
		result.Actions = append(result.Actions, "settlement_hold")
	}
	return result
}

func inProgressState(state string) bool {
	switch state {
	case "READY", "IN_PROGRESS", "REVISION_REQUESTED", "REQUIREMENT_PENDING":
		return true
	}
	return false
}

func ratio(part, total int) float64 {
	if total <= 0 {
		return 0
	}
	return float64(part) / float64(total)
}

func containsLevel(levels []string, level string) bool {
	for _, item := range levels {
		if item == level {
			return true
		}
	}
	return false
}

// scanRisk rescores live orders. A new row is stored only when the level moves
// or the score shifts materially, so the history stays readable instead of
// growing by one row per order per scan.
func (w *Worker) scanRisk(ctx context.Context, current policy) int {
	if !current.RiskEnabled {
		return 0
	}
	rows, err := w.DB.Query(ctx, `SELECT o.id,o.buyer_id,o.seller_id,o.amount,o.state,o.due_at,
		EXTRACT(EPOCH FROM (now()-bu.created_at))/86400,
		(SELECT count(*) FROM orders b WHERE b.buyer_id=o.buyer_id AND b.created_at > now()-interval '24 hours'),
		(SELECT count(*) FROM orders b WHERE b.buyer_id=o.buyer_id),
		(SELECT count(*) FROM disputes d JOIN orders b ON b.id=d.order_id WHERE b.buyer_id=o.buyer_id),
		(SELECT count(*) FROM orders sx WHERE sx.seller_id=o.seller_id AND sx.state='COMPLETED'),
		(SELECT count(*) FROM orders sx WHERE sx.seller_id=o.seller_id),
		(SELECT count(*) FROM disputes d JOIN orders sx ON sx.id=d.order_id WHERE sx.seller_id=o.seller_id),
		(SELECT count(*) FROM revisions rv WHERE rv.order_id=o.id)
		FROM orders o JOIN users bu ON bu.id=o.buyer_id
		WHERE o.state NOT IN ('COMPLETED','CANCELLED','REFUNDED') ORDER BY o.updated_at DESC LIMIT $1`, current.RiskBatch)
	if err != nil {
		if ctx.Err() == nil {
			w.Logger.Error("risk scan query failed", "error", err)
		}
		return 0
	}
	facts := make([]orderFacts, 0, current.RiskBatch)
	for rows.Next() {
		var item orderFacts
		var ageDays float64
		if rows.Scan(&item.OrderID, &item.BuyerID, &item.SellerID, &item.Amount, &item.State, &item.DueAt, &ageDays,
			&item.BuyerOrders24h, &item.BuyerOrders, &item.BuyerDisputes, &item.SellerCompleted, &item.SellerOrders, &item.SellerDisputes, &item.Revisions) != nil {
			continue
		}
		item.BuyerAgeDays = ageDays
		facts = append(facts, item)
	}
	rows.Close()
	now := time.Now()
	stored := 0
	for _, item := range facts {
		if ctx.Err() != nil {
			return stored
		}
		assessment := assessOrder(item, current.Risk, now)
		var previousLevel string
		var previousScore float64
		err := w.DB.QueryRow(ctx, `SELECT level,score FROM risk_scores WHERE resource_type='order' AND resource_id=$1 ORDER BY calculated_at DESC LIMIT 1`, item.OrderID).Scan(&previousLevel, &previousScore)
		if err == nil && previousLevel == assessment.Level && abs(previousScore-float64(assessment.Score)) < 5 {
			continue
		}
		if _, err := w.DB.Exec(ctx, `INSERT INTO risk_scores(id,resource_type,resource_id,level,score,signals,actions,model_version) VALUES($1,'order',$2,$3,$4,$5,$6,$7)`,
			uuid.New(), item.OrderID, assessment.Level, assessment.Score, assessment.Signals, assessment.Actions, riskModelVersion); err != nil {
			if ctx.Err() == nil {
				w.Logger.Error("risk score write failed", "error", err, "order_id", item.OrderID)
			}
			continue
		}
		stored++
	}
	if stored > 0 {
		w.Logger.Info("risk scan stored scores", "orders", len(facts), "changed", stored)
	}
	return stored
}

func abs(value float64) float64 {
	if value < 0 {
		return -value
	}
	return value
}
