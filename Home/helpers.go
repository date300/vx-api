package Home

import (
	"fmt"
	"strings"
	"vx-api/Config"
	"vx-api/Utils"

	"github.com/gin-gonic/gin"
)

// enrichVideos adds is_liked, is_following, is_follow_back to video list
func enrichVideos(userID uint, videos []Video) []gin.H {
	result := make([]gin.H, 0, len(videos))
	if len(videos) == 0 {
		return result
	}

	var likedVideoIDs, followingIDs, followBackIDs map[uint]bool
	likedVideoIDs = map[uint]bool{}
	followingIDs = map[uint]bool{}
	followBackIDs = map[uint]bool{}

	if userID != 0 {
		videoIDs := make([]uint, 0, len(videos))
		uploaderIDs := make([]uint, 0, len(videos))
		for _, v := range videos {
			videoIDs = append(videoIDs, v.ID)
			uploaderIDs = append(uploaderIDs, v.UserID)
		}

		var likes []Like
		Config.DB.Where("user_id = ? AND video_id IN ?", userID, videoIDs).Find(&likes)
		for _, l := range likes {
			likedVideoIDs[l.VideoID] = true
		}

		var following []Follow
		Config.DB.Where("follower_id = ? AND following_id IN ?", userID, uploaderIDs).Find(&following)
		for _, f := range following {
			followingIDs[f.FollowingID] = true
		}

		var followBack []Follow
		Config.DB.Where("follower_id IN ? AND following_id = ?", uploaderIDs, userID).Find(&followBack)
		for _, f := range followBack {
			followBackIDs[f.FollowerID] = true
		}
	}

	for _, v := range videos {
		// Filter out videos with missing physical files
		if !strings.HasPrefix(v.URL, "http") && !Utils.FileExists(v.URL) {
			fmt.Printf("DEBUG: Skipping video %d due to missing file: %s\n", v.ID, v.URL)
			continue
		}

		var soundData interface{}
		if v.SoundID != nil {
			var s struct {
				ID           uint   `json:"id"`
				Title        string `json:"title"`
				AuthorName   string `json:"author_name"`
				AuthorAvatar string `json:"author_avatar"`
				AudioURL     string `json:"audio_url"`
			}
			if err := Config.DB.Table("sounds").Where("id = ?", *v.SoundID).Limit(1).Find(&s).Error; err == nil && s.ID != 0 {
				soundData = s
			}
		}

		isSaved := false
		if userID != 0 {
			var existingSave Save
			if tx := Config.DB.Where("user_id = ? AND video_id = ?", userID, v.ID).Limit(1).Find(&existingSave); tx.RowsAffected > 0 {
				isSaved = true
			}
		}

		result = append(result, gin.H{
			"id":             v.ID,
			"user_id":        v.UserID,
			"user":           v.User,
			"url":            v.URL,
			"caption":        v.Caption,
			"sound":          v.Sound,
			"sound_id":       v.SoundID,
			"sound_data":     soundData,
			"duration":       v.Duration,
			"thumbnail_url":  v.ThumbnailURL,
			"status":         v.Status,
			"likes":          v.Likes,
			"comments":       v.Comments,
			"views":          v.Views,
			"shares":         v.Shares,
			"is_image":       v.IsImage,
			"images":         v.Images,
			"is_ad":          v.IsAd,
			"ad_cta":         v.AdCta,
			"ad_link":        v.AdLink,
			"filter":         v.Filter,
			"speed":          v.Speed,
			"allow_comments": v.AllowComments,
			"allow_duet":     v.AllowDuet,
			"allow_save":     v.AllowSave,
			"is_public":      v.IsPublic,
			"location":       v.Location,
			"text_overlays":  v.TextOverlays,
			"created_at":     v.CreatedAt,
			"is_liked":       likedVideoIDs[v.ID],
			"is_saved":       isSaved,
			"is_following":   followingIDs[v.UserID],
			"is_follow_back": followBackIDs[v.UserID],
		})
	}
	return result
}

// getUserIDOrZero extracts userID from context, returns 0 if not found
func getUserIDOrZero(c *gin.Context) uint {
	if v, exists := c.Get("userID"); exists {
		if uid, ok := v.(uint); ok {
			return uid
		}
	}
	return 0
}
