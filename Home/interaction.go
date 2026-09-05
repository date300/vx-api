package Home

import (
	"net/http"
	"time"
	"vx-api/Config"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

var InteractionWeights = map[string]float64{
	"WATCH":          1.0,
	"WATCH_END":      0.5,
	"COMPLETION":     5.0,
	"REWATCH":        3.0,
	"LIKE":           3.0,
	"UNLIKE":         -3.0,
	"COMMENT":        4.0,
	"SHARE":          5.0,
	"SAVE":           6.0,
	"UNSAVE":         -6.0,
	"FOLLOW":         10.0,
	"PROFILE_VISIT":  2.0,
	"SKIP":           -5.0,
	"FAST_SCROLL":    -2.0,
	"NOT_INTERESTED": -10.0,
	"REPORT":         -20.0,
}

func TrackInteraction(c *gin.Context) {
	var input Interaction
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": false, "message": "Invalid interaction data"})
		return
	}

	userIDRaw, _ := c.Get("userID")
	userID, ok := userIDRaw.(uint)
	if !ok {
		// Just acknowledge for guests or invalid states, don't store or process for recommendations
		c.JSON(http.StatusOK, gin.H{"status": true, "message": "Interaction acknowledged (guest/no-uid)"})
		return
	}

	input.UserID = userID
	input.CreatedAt = time.Now()

	if err := Config.DB.Create(&input).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": false, "message": "Failed to track interaction"})
		return
	}

	// Async update user interests and video stats
	go processInteraction(input)

	c.JSON(http.StatusOK, gin.H{"status": true, "message": "Interaction tracked"})
}

func processInteraction(inter Interaction) {
	// 1. Update Video Stats (Bucket views, etc.)
	updateVideoStats(inter)

	// 2. Update User Interest Profile
	updateUserInterest(inter)
}

func updateVideoStats(inter Interaction) {
	var video Video
	// Use Find with Where to avoid GORM's ErrRecordNotFound logging
	if err := Config.DB.Where("id = ?", inter.VideoID).Limit(1).Find(&video).Error; err != nil || video.ID == 0 {
		return
	}

	updates := make(map[string]interface{})

	newViews := video.Views
	newBucketViews := video.BucketViews

	if inter.Type == "WATCH" {
		newViews++
		newBucketViews++
		updates["views"] = newViews
		updates["bucket_views"] = newBucketViews
	}

	// Recalculate EngagementScore with more weight on completions
	totalEngagement := float64(video.Likes)*2.0 + float64(video.Comments)*4.0 + float64(video.Shares)*5.0
	// We could also add completions to the engagement score if we had an aggregate field.
	// For now, let's keep it simple.
	if newViews > 0 {
		updates["engagement_score"] = totalEngagement / float64(newViews)
	}

	// Simple bucket promotion logic
	if video.Bucket == 0 && newBucketViews >= 100 {
		updates["bucket"] = 1
		updates["bucket_views"] = 0
	} else if video.Bucket == 1 && newBucketViews >= 1000 {
		if (totalEngagement / float64(newViews)) > 0.1 {
			updates["bucket"] = 2
			updates["bucket_views"] = 0
		}
	}

	if len(updates) > 0 {
		Config.DB.Model(&video).Updates(updates)
	}
}

func updateUserInterest(inter Interaction) {
	weight, ok := InteractionWeights[inter.Type]
	if !ok {
		return
	}

	// Get video's category
	var video Video
	// Use Find with Where to avoid noisy logs
	if err := Config.DB.Select("category_id").Where("id = ?", inter.VideoID).Limit(1).Find(&video).Error; err != nil || video.ID == 0 || video.CategoryID == nil {
		return
	}

	var interest RecommendationInterest
	res := Config.DB.Where("user_id = ? AND category_id = ?", inter.UserID, *video.CategoryID).Limit(1).Find(&interest)

	if res.RowsAffected == 0 {
		Config.DB.Create(&RecommendationInterest{
			UserID:     inter.UserID,
			CategoryID: *video.CategoryID,
			Score:      weight,
			UpdatedAt:  time.Now(),
		})
	} else {
		Config.DB.Model(&interest).UpdateColumn("score", gorm.Expr("score + ?", weight))
	}
}
