package Home

import (
	"fmt"
	"math/rand"
	"net/http"
	"sort"
	"strings"
	"time"
	"vx-api/Algorithms"
	"vx-api/Config"
	"vx-api/Utils"

	"github.com/gin-gonic/gin"
)

// GetForYouVideos handles GET /api/v1/home/foryou
func GetForYouVideos(c *gin.Context) {
	var page, limit int
	fmt.Sscanf(c.Query("page"), "%d", &page)
	fmt.Sscanf(c.Query("limit"), "%d", &limit)

	if page <= 0 {
		page = 1
	}
	if limit <= 0 || limit > 100 {
		limit = 10
	}
	offset := (page - 1) * limit

	userID := getUserIDOrZero(c)
	interests := make(map[uint]float64)
	if userID != 0 {
		var userInterests []RecommendationInterest
		Config.DB.Where("user_id = ?", userID).Find(&userInterests)
		for _, ui := range userInterests {
			interests[ui.CategoryID] = ui.Score
		}
	}

	var rawCandidates []Video
	// Fetch raw data without preload (GORM Raw doesn't support Preload properly)
	err := Config.DB.Raw(`
			(SELECT * FROM videos WHERE status != 'story' AND deleted_at IS NULL ORDER BY created_at DESC LIMIT 300)
			UNION
			(SELECT * FROM videos WHERE status != 'story' AND deleted_at IS NULL ORDER BY ranking_score DESC LIMIT 200)
		`).Scan(&rawCandidates).Error

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"status":  false,
			"message": "Failed to load candidates",
		})
		return
	}

	// Filter out videos with missing physical files and collect IDs
	var validIDs []uint
	for _, v := range rawCandidates {
		if strings.HasPrefix(v.URL, "http://") || strings.HasPrefix(v.URL, "https://") || Utils.FileExists(v.URL) {
			validIDs = append(validIDs, v.ID)
		} else {
			fmt.Printf("DEBUG: File not found for video %d: %s\n", v.ID, v.URL)
		}
	}
	fmt.Printf("DEBUG: Serving %d valid videos\n", len(validIDs))

	// Now fetch full video objects with Preload using GORM's Find
	var candidates []Video
	if len(validIDs) > 0 {
		// Use a map to maintain the original order from Raw query if needed,
		// but for now, just fetch them. The scoring happens next anyway.
		if err := Config.DB.Preload("User").Where("id IN ?", validIDs).Find(&candidates).Error; err != nil {
			fmt.Printf("DEBUG: Preload User Error: %v\n", err)
		}
	}

	type scoredVideo struct {
		video Video
		score float64
	}
	scored := make([]scoredVideo, 0, len(candidates))
	rnd := rand.New(rand.NewSource(time.Now().UnixNano()))

	for _, v := range candidates {
		base := Algorithms.CalculateBaseScore(v.Likes, v.Comments, v.Shares, v.Views, v.CreatedAt)
		personal := Algorithms.CalculatePersonalizedScore(base, v.CategoryID, interests)
		// High randomness (Exploration) - 30% random variance to give everyone a chance
		exploration := 0.85 + (rnd.Float64() * 0.3)
		final := personal * exploration
		scored = append(scored, scoredVideo{v, final})
	}

	sort.Slice(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})

	var videos []Video
	start := offset
	end := offset + limit
	if start < len(scored) {
		if end > len(scored) {
			end = len(scored)
		}
		for i := start; i < end; i++ {
			videos = append(videos, scored[i].video)
		}
	}

	c.Header("Cache-Control", "no-store, no-cache, must-revalidate, proxy-revalidate, max-age=0")
	c.Header("Pragma", "no-cache")
	c.Header("Expires", "0")

	c.JSON(http.StatusOK, gin.H{
		"status":  true,
		"message": "For You feed loaded successfully",
		"data":    enrichVideos(userID, videos),
	})
}

// GetFollowingVideos handles GET /api/v1/home/following
func GetFollowingVideos(c *gin.Context) {
	userIDRaw, exists := c.Get("userID")
	if !exists {
		c.JSON(http.StatusOK, gin.H{"status": true, "data": []interface{}{}})
		return
	}
	uid := userIDRaw.(uint)

	var page, limit int
	fmt.Sscanf(c.Query("page"), "%d", &page)
	fmt.Sscanf(c.Query("limit"), "%d", &limit)

	if page <= 0 {
		page = 1
	}
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	offset := (page - 1) * limit

	var followingIDs []uint
	Config.DB.Model(&Follow{}).Where("follower_id = ?", uid).Pluck("following_id", &followingIDs)

	var videos []Video
	if len(followingIDs) > 0 {
		var rawVideos []Video
		if err := Config.DB.Preload("User").
			Where("user_id IN ? AND status != ?", followingIDs, "story").
			Order("created_at desc").
			Limit(limit * 2).
			Offset(offset).
			Find(&rawVideos).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"status": false, "message": "Failed to load following feed"})
			return
		}

		for _, v := range rawVideos {
			if strings.HasPrefix(v.URL, "http://") || strings.HasPrefix(v.URL, "https://") || Utils.FileExists(v.URL) {
				videos = append(videos, v)
				if len(videos) >= limit {
					break
				}
			}
		}
	}

	c.Header("Cache-Control", "no-store, no-cache, must-revalidate, proxy-revalidate, max-age=0")
	c.Header("Pragma", "no-cache")
	c.Header("Expires", "0")

	c.JSON(http.StatusOK, gin.H{
		"status": true,
		"data":   enrichVideos(uid, videos),
	})
}

// GetFriendsVideos handles GET /api/v1/home/friends
func GetFriendsVideos(c *gin.Context) {
	userIDRaw, exists := c.Get("userID")
	if !exists {
		c.JSON(http.StatusOK, gin.H{"status": true, "data": []interface{}{}})
		return
	}
	uid := userIDRaw.(uint)

	var page, limit int
	fmt.Sscanf(c.Query("page"), "%d", &page)
	fmt.Sscanf(c.Query("limit"), "%d", &limit)

	if page <= 0 {
		page = 1
	}
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	offset := (page - 1) * limit

	var friendIDs []uint
	queryRaw := `
		SELECT f1.following_id
		FROM follows f1
		INNER JOIN follows f2 ON f1.following_id = f2.follower_id
		WHERE f1.follower_id = ? AND f2.following_id = ?
	`
	Config.DB.Raw(queryRaw, uid, uid).Scan(&friendIDs)

	var videos []Video
	if len(friendIDs) > 0 {
		var rawVideos []Video
		if err := Config.DB.Preload("User").
			Where("user_id IN ? AND status != ?", friendIDs, "story").
			Order("created_at desc").
			Limit(limit * 2).
			Offset(offset).
			Find(&rawVideos).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"status": false, "message": "Failed to load friends feed"})
			return
		}

		for _, v := range rawVideos {
			if strings.HasPrefix(v.URL, "http://") || strings.HasPrefix(v.URL, "https://") || Utils.FileExists(v.URL) {
				videos = append(videos, v)
				if len(videos) >= limit {
					break
				}
			}
		}
	}

	c.Header("Cache-Control", "no-store, no-cache, must-revalidate, proxy-revalidate, max-age=0")
	c.Header("Pragma", "no-cache")
	c.Header("Expires", "0")

	c.JSON(http.StatusOK, gin.H{
		"status": true,
		"data":   enrichVideos(uid, videos),
	})
}

// GetStories handles GET /api/v1/home/stories
func GetStories(c *gin.Context) {
	userIDRaw, exists := c.Get("userID")
	if !exists {
		c.JSON(http.StatusOK, gin.H{"status": true, "data": []interface{}{}, "message": "Guest state"})
		return
	}
	uid := userIDRaw.(uint)

	var followingIDs []uint
	Config.DB.Model(&Follow{}).Where("follower_id = ?", uid).Pluck("following_id", &followingIDs)
	followingIDs = append(followingIDs, uid)

	var stories []Video
	if err := Config.DB.Preload("User").
		Where("user_id IN ? AND status = ?", followingIDs, "story").
		Order("created_at desc").
		Find(&stories).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": false, "message": "Failed to load stories"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":  true,
		"message": "Stories loaded successfully",
		"data":    enrichVideos(uid, stories),
	})
}

// GetVideoById handles GET /api/v1/video/:id
func GetVideoById(c *gin.Context) {
	videoID := c.Param("id")
	userID := getUserIDOrZero(c)

	var video Video
	if err := Config.DB.Preload("User").Where("id = ?", videoID).First(&video).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"status": false, "message": "Video not found"})
		return
	}

	enriched := enrichVideos(userID, []Video{video})
	if len(enriched) == 0 {
		c.JSON(http.StatusNotFound, gin.H{"status": false, "message": "Video not available (missing file)"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status": true,
		"data":   enriched[0],
	})
}
