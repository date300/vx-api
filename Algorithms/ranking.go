package Algorithms

import (
	"math"
	"time"
	"gorm.io/gorm"
)

// CalculateBaseScore computes a score for a video.
// Optimized for "Easy Distribution" where everyone gets a chance.
func CalculateBaseScore(likes, comments, shares int, views int64, createdAt time.Time) float64 {
	fLikes := float64(likes)
	fComments := float64(comments)
	fShares := float64(shares)

	// Base score of 100 ensures even 0-view videos have a starting point
	score := 100.0

	// Add engagement weight (Less aggressive than before)
	// Likes: 5, Comments: 10, Shares: 15
	engagement := (fLikes * 5.0) + (fComments * 10.0) + (fShares * 15.0)
	score += engagement

	// Freshness is KEY for "Easy Foryou"
	// We use a much gentler decay (1.2 instead of 1.8)
	hoursSinceUpload := time.Since(createdAt).Hours()
	freshness := 1.0 / math.Pow(hoursSinceUpload+1, 1.2)

	// New Video Boost: First 24 hours get a 2x multiplier
	if hoursSinceUpload < 24 {
		freshness *= 2.0
	}

	return score * freshness
}

// CalculatePersonalizedScore adjusts the base score based on user interests
func CalculatePersonalizedScore(baseScore float64, categoryID *uint, userInterests map[uint]float64) float64 {
	if categoryID == nil {
		return baseScore
	}

	interestScore, ok := userInterests[*categoryID]
	if !ok {
		return baseScore
	}

	// Adjust multiplier to be less restrictive
	multiplier := 1.0 + (interestScore / 200.0) // Gentler personalization
	return baseScore * multiplier
}

// UpdateGlobalRankingScores updates the ranking_score for all videos
func UpdateGlobalRankingScores(db *gorm.DB) error {
	return db.Exec(`
		UPDATE videos
		SET ranking_score = (
			(100.0 + CAST(likes AS FLOAT) * 5.0 + CAST(comments AS FLOAT) * 10.0 + CAST(shares AS FLOAT) * 15.0) *
			(CASE WHEN EXTRACT(EPOCH FROM (NOW() - created_at))/3600 < 24 THEN 2.0 ELSE 1.0 END) *
			(1.0 / POW(EXTRACT(EPOCH FROM (NOW() - created_at))/3600 + 1, 1.2))
		)
		WHERE status != 'story' AND deleted_at IS NULL
	`).Error
}
