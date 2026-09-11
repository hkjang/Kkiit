package worker

import (
	"context"

	"github.com/google/uuid"
)

const trustAlgorithmVersion = "trust-1"

// sellerFacts holds the delivery record a trust score is derived from. Every
// number comes from completed transactions, never from self reported data.
type sellerFacts struct {
	SellerID      uuid.UUID
	Completed     int
	Cancelled     int
	TotalOrders   int
	OnTime        int
	DueMeasured   int
	Rating        float64
	RatingCount   int
	Disputes      int
	ResponseHours *float64
}

type sellerLevel struct {
	Code      string `json:"code"`
	MinScore  int    `json:"min_score"`
	MinOrders int    `json:"min_orders"`
}

func defaultSellerLevels() []sellerLevel {
	return []sellerLevel{
		{Code: "ELITE", MinScore: 85, MinOrders: 20},
		{Code: "PRO", MinScore: 70, MinOrders: 8},
		{Code: "RISING", MinScore: 50, MinOrders: 3},
		{Code: "NEW", MinScore: 0, MinOrders: 0},
	}
}

type sellerScore struct {
	Score      float64
	Level      string
	Components map[string]float64
}

// scoreSeller weights the record out of 100. A seller with no history gets the
// neutral half of each component rather than a zero, so a new account is not
// buried below one with a bad record.
func scoreSeller(facts sellerFacts, levels []sellerLevel) sellerScore {
	components := map[string]float64{}

	rating := 20.0
	if facts.RatingCount > 0 {
		rating = facts.Rating / 5 * 40
	}
	components["rating"] = round2(rating)

	onTime := 10.0
	if facts.DueMeasured > 0 {
		onTime = float64(facts.OnTime) / float64(facts.DueMeasured) * 20
	}
	components["on_time"] = round2(onTime)

	completion := 8.0
	if finished := facts.Completed + facts.Cancelled; finished > 0 {
		completion = float64(facts.Completed) / float64(finished) * 15
	}
	components["completion"] = round2(completion)

	disputeFree := 5.0
	if facts.TotalOrders > 0 {
		disputeFree = (1 - ratio(facts.Disputes, facts.TotalOrders)) * 10
		if disputeFree < 0 {
			disputeFree = 0
		}
	}
	components["dispute_free"] = round2(disputeFree)

	response := 5.0
	if facts.ResponseHours != nil {
		switch hours := *facts.ResponseHours; {
		case hours <= 4:
			response = 10
		case hours <= 24:
			response = 6
		case hours <= 72:
			response = 3
		default:
			response = 1
		}
	}
	components["response"] = round2(response)

	volume := float64(minInt(facts.Completed, 20)) / 20 * 5
	components["volume"] = round2(volume)

	total := rating + onTime + completion + disputeFree + response + volume
	if total > 100 {
		total = 100
	}
	return sellerScore{Score: round2(total), Level: gradeSeller(total, facts.Completed, levels), Components: components}
}

// gradeSeller needs both the score and the delivered volume so a single glowing
// review cannot promote an account past sellers with a real track record.
func gradeSeller(score float64, completed int, levels []sellerLevel) string {
	best := "NEW"
	bestScore := -1
	for _, level := range levels {
		if score >= float64(level.MinScore) && completed >= level.MinOrders && level.MinScore > bestScore {
			best, bestScore = level.Code, level.MinScore
		}
	}
	return best
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func round2(value float64) float64 {
	return float64(int64(value*100+0.5)) / 100
}

func (w *Worker) scoreSellers(ctx context.Context, current policy) int {
	if !current.TrustEnabled {
		return 0
	}
	rows, err := w.DB.Query(ctx, `SELECT sp.user_id,
		(SELECT count(*) FROM orders o WHERE o.seller_id=sp.user_id AND o.state='COMPLETED'),
		(SELECT count(*) FROM orders o WHERE o.seller_id=sp.user_id AND o.state IN ('CANCELLED','REFUNDED')),
		(SELECT count(*) FROM orders o WHERE o.seller_id=sp.user_id),
		(SELECT count(*) FROM orders o WHERE o.seller_id=sp.user_id AND o.state='COMPLETED' AND o.due_at IS NOT NULL AND o.accepted_at IS NOT NULL AND o.accepted_at<=o.due_at),
		(SELECT count(*) FROM orders o WHERE o.seller_id=sp.user_id AND o.state='COMPLETED' AND o.due_at IS NOT NULL AND o.accepted_at IS NOT NULL),
		COALESCE((SELECT avg((r.quality+r.communication+r.timeliness+r.professionalism)::numeric/4) FROM reviews r WHERE r.seller_id=sp.user_id),0),
		(SELECT count(*) FROM reviews r WHERE r.seller_id=sp.user_id),
		(SELECT count(*) FROM disputes d JOIN orders o ON o.id=d.order_id WHERE o.seller_id=sp.user_id),
		(SELECT avg(EXTRACT(EPOCH FROM (fm.first_at-o.created_at))/3600) FROM orders o
			JOIN LATERAL (SELECT min(m.created_at) AS first_at FROM messages m WHERE m.order_id=o.id AND m.sender_id=o.seller_id) fm ON true
			WHERE o.seller_id=sp.user_id AND fm.first_at IS NOT NULL)
		FROM seller_profiles sp ORDER BY sp.updated_at DESC LIMIT $1`, current.TrustBatch)
	if err != nil {
		if ctx.Err() == nil {
			w.Logger.Error("seller score query failed", "error", err)
		}
		return 0
	}
	facts := make([]sellerFacts, 0, current.TrustBatch)
	for rows.Next() {
		var item sellerFacts
		if rows.Scan(&item.SellerID, &item.Completed, &item.Cancelled, &item.TotalOrders, &item.OnTime, &item.DueMeasured,
			&item.Rating, &item.RatingCount, &item.Disputes, &item.ResponseHours) != nil {
			continue
		}
		facts = append(facts, item)
	}
	rows.Close()
	updated := 0
	for _, item := range facts {
		if ctx.Err() != nil {
			return updated
		}
		score := scoreSeller(item, current.SellerLevels)
		if _, err := w.DB.Exec(ctx, `UPDATE seller_profiles SET score=$2,level=$3,rating=$4,rating_count=$5,scored_at=now() WHERE user_id=$1`,
			item.SellerID, score.Score, score.Level, round2(item.Rating), item.RatingCount); err != nil {
			if ctx.Err() == nil {
				w.Logger.Error("seller score update failed", "error", err, "seller_id", item.SellerID)
			}
			continue
		}
		// The history table only gains a row when the score actually moves.
		var previous float64
		err := w.DB.QueryRow(ctx, `SELECT score FROM seller_scores WHERE seller_id=$1 ORDER BY calculated_at DESC LIMIT 1`, item.SellerID).Scan(&previous)
		if err == nil && abs(previous-score.Score) < 1 {
			continue
		}
		if _, err := w.DB.Exec(ctx, `INSERT INTO seller_scores(id,seller_id,score,components,algorithm_version) VALUES($1,$2,$3,$4,$5)`,
			uuid.New(), item.SellerID, score.Score, score.Components, trustAlgorithmVersion); err != nil && ctx.Err() == nil {
			w.Logger.Error("seller score history failed", "error", err, "seller_id", item.SellerID)
		}
		updated++
	}
	return updated
}

// talentFacts describes one published product. Quality mixes who is selling it
// with how complete the listing is and how buyers have actually responded.
type talentFacts struct {
	TalentID     uuid.UUID
	SellerScore  float64
	Description  int
	Packages     int
	FAQ          int
	Portfolios   int
	Orders       int
	Favorites    int
	Rating       float64
	RatingCount  int
	PublishedAge float64
}

func scoreTalent(facts talentFacts) (float64, map[string]float64) {
	components := map[string]float64{}
	components["seller"] = round2(facts.SellerScore * 0.4)

	completeness := 0.0
	if facts.Description >= 200 {
		completeness += 8
	} else if facts.Description >= 80 {
		completeness += 4
	}
	if facts.Packages >= 2 {
		completeness += 4
	} else if facts.Packages >= 1 {
		completeness += 2
	}
	if facts.FAQ >= 1 {
		completeness += 4
	}
	// A seller who shows past work gives buyers something to judge beyond copy.
	if facts.Portfolios >= 3 {
		completeness += 4
	} else if facts.Portfolios >= 1 {
		completeness += 2
	}
	components["completeness"] = completeness

	demand := float64(minInt(facts.Orders, 10))/10*10 + float64(minInt(facts.Favorites, 20))/20*5
	components["demand"] = round2(demand)

	satisfaction := 7.5
	if facts.RatingCount > 0 {
		satisfaction = facts.Rating / 5 * 15
	}
	components["satisfaction"] = round2(satisfaction)

	freshness := 0.0
	switch {
	case facts.PublishedAge <= 30:
		freshness = 10
	case facts.PublishedAge <= 90:
		freshness = 5
	}
	components["freshness"] = freshness

	total := components["seller"] + completeness + demand + satisfaction + freshness
	if total > 100 {
		total = 100
	}
	return round2(total), components
}

func (w *Worker) scoreTalents(ctx context.Context, current policy) int {
	if !current.TrustEnabled {
		return 0
	}
	rows, err := w.DB.Query(ctx, `SELECT t.id,COALESCE(sp.score,0),length(t.description),
		(SELECT count(*) FROM talent_packages p WHERE p.talent_id=t.id AND p.active),
		COALESCE(jsonb_array_length(t.faq),0),
		(SELECT count(*) FROM portfolios pf WHERE pf.owner_id=t.seller_id),
		(SELECT count(*) FROM orders o WHERE o.talent_id=t.id),
		(SELECT count(*) FROM favorites f WHERE f.talent_id=t.id),
		COALESCE((SELECT avg((r.quality+r.communication+r.timeliness+r.professionalism)::numeric/4) FROM reviews r JOIN orders o ON o.id=r.order_id WHERE o.talent_id=t.id),0),
		(SELECT count(*) FROM reviews r JOIN orders o ON o.id=r.order_id WHERE o.talent_id=t.id),
		COALESCE(EXTRACT(EPOCH FROM (now()-t.published_at))/86400,9999)
		FROM talents t LEFT JOIN seller_profiles sp ON sp.user_id=t.seller_id
		WHERE t.status='published' ORDER BY t.updated_at DESC LIMIT $1`, current.TrustBatch)
	if err != nil {
		if ctx.Err() == nil {
			w.Logger.Error("talent score query failed", "error", err)
		}
		return 0
	}
	facts := make([]talentFacts, 0, current.TrustBatch)
	for rows.Next() {
		var item talentFacts
		if rows.Scan(&item.TalentID, &item.SellerScore, &item.Description, &item.Packages, &item.FAQ, &item.Portfolios, &item.Orders, &item.Favorites,
			&item.Rating, &item.RatingCount, &item.PublishedAge) != nil {
			continue
		}
		facts = append(facts, item)
	}
	rows.Close()
	updated := 0
	for _, item := range facts {
		if ctx.Err() != nil {
			return updated
		}
		score, components := scoreTalent(item)
		if _, err := w.DB.Exec(ctx, `UPDATE talents SET quality_score=$2 WHERE id=$1`, item.TalentID, score); err != nil {
			if ctx.Err() == nil {
				w.Logger.Error("talent score update failed", "error", err, "talent_id", item.TalentID)
			}
			continue
		}
		var previous float64
		err := w.DB.QueryRow(ctx, `SELECT score FROM talent_scores WHERE talent_id=$1 ORDER BY calculated_at DESC LIMIT 1`, item.TalentID).Scan(&previous)
		if err == nil && abs(previous-score) < 1 {
			continue
		}
		if _, err := w.DB.Exec(ctx, `INSERT INTO talent_scores(id,talent_id,score,components,result,algorithm_version) VALUES($1,$2,$3,$4,$5,$6)`,
			uuid.New(), item.TalentID, score, components, qualityBand(score), trustAlgorithmVersion); err != nil && ctx.Err() == nil {
			w.Logger.Error("talent score history failed", "error", err, "talent_id", item.TalentID)
		}
		updated++
	}
	return updated
}

func qualityBand(score float64) string {
	switch {
	case score >= 80:
		return "excellent"
	case score >= 60:
		return "good"
	case score >= 40:
		return "fair"
	default:
		return "needs_work"
	}
}
