package Comment

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"time"
	"vx-api/Config"
	"vx-api/Home"
	"vx-api/Middleware"
	"vx-api/Profile"
	"vx-api/Realtime"
	"vx-api/Utils"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Comment Model with Reply support
type Comment struct {
	ID        uint           `gorm:"primaryKey" json:"id"`
	VideoID   uint           `gorm:"index;not null" json:"video_id"`
	UserID    uint           `gorm:"index;not null" json:"user_id"`
	User      Profile.User   `gorm:"foreignKey:UserID" json:"user"`
	Text       string         `gorm:"type:text;not null" json:"text"`
	StickerURL *string        `gorm:"type:text" json:"sticker_url"`
	StickerID     *uint          `gorm:"index" json:"sticker_id"`
	IsVoice       bool           `gorm:"default:false" json:"is_voice"`
	VoiceURL      *string        `gorm:"type:text" json:"voice_url"`
	VoiceDuration int            `gorm:"default:0" json:"voice_duration"`
	ParentID      *uint          `gorm:"index" json:"parent_id"` // NULL for top-level comments
	IsPinned   bool           `gorm:"default:false" json:"is_pinned"`
	IsFirst    bool           `gorm:"default:false" json:"is_first"`
	Likes      int            `gorm:"default:0" json:"likes"`
	Replies    []Comment      `gorm:"foreignKey:ParentID" json:"replies,omitempty"`
	Liked      bool           `gorm:"-" json:"liked"`
	CreatedAt  time.Time      `json:"created_at"`
	UpdatedAt  time.Time      `json:"updated_at"`
	DeletedAt  gorm.DeletedAt `gorm:"index" json:"-"`
}

func (Comment) TableName() string {
	return "comments"
}

// CommentLike Model
type CommentLike struct {
	ID        uint `gorm:"primaryKey" json:"id"`
	UserID    uint `gorm:"not null;uniqueIndex:idx_user_comment_like" json:"user_id"`
	CommentID uint `gorm:"not null;uniqueIndex:idx_user_comment_like" json:"comment_id"`
}

func (CommentLike) TableName() string {
	return "comment_likes"
}

// RegisterRoutes registers comment related endpoints
func RegisterRoutes(r *gin.RouterGroup) {
	commentGroup := r.Group("/video")
	{
		commentGroup.GET("/:id/comments", Middleware.OptionalAuth(), GetComments)
		commentGroup.POST("/:id/comment", Middleware.AuthRequired(), PostComment)
		commentGroup.POST("/:id/comment/voice", Middleware.AuthRequired(), PostVoiceComment)
		commentGroup.POST("/comment/:id/like", Middleware.AuthRequired(), ToggleCommentLike)
		commentGroup.DELETE("/comment/:id", Middleware.AuthRequired(), DeleteComment)
		commentGroup.POST("/comment/:id/pin", Middleware.AuthRequired(), PinComment)
	}
}

// GetComments handles GET /api/v1/video/:id/comments
func GetComments(c *gin.Context) {
	videoID := c.Param("id")
	userID := getUserIDOrZero(c)

	var comments []Comment
	// Sort by is_pinned DESC first, then created_at DESC
	if err := Config.DB.Preload("User").Preload("Replies.User").
		Where("video_id = ? AND parent_id IS NULL", videoID).
		Order("is_pinned DESC, created_at DESC").Find(&comments).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": false, "message": "Failed to load comments"})
		return
	}

	// Helper to enrich comments with liked status
	enrichWithLikedStatus(userID, comments)

	c.JSON(http.StatusOK, gin.H{
		"status":   true,
		"comments": comments,
	})
}

// PostComment handles POST /api/v1/video/:id/comment
func PostComment(c *gin.Context) {
	videoIDStr := c.Param("id")
	userID, _ := c.Get("userID")

	var input struct {
		Text       string  `json:"text"`
		StickerURL *string `json:"sticker_url"`
		StickerID  *uint   `json:"sticker_id"`
		ParentID   *uint   `json:"parent_id"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": false, "message": "Bad request"})
		return
	}

	if input.Text == "" && input.StickerURL == nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": false, "message": "Comment or sticker required"})
		return
	}

	var videoID uint
	if _, err := fmt.Sscanf(videoIDStr, "%d", &videoID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": false, "message": "Invalid video ID"})
		return
	}

	// Check if this is the first top-level comment for this video
	var count int64
	Config.DB.Model(&Comment{}).Where("video_id = ? AND parent_id IS NULL", videoID).Count(&count)
	isFirst := count == 0 && input.ParentID == nil

	comment := Comment{
		VideoID:    videoID,
		UserID:     userID.(uint),
		Text:       input.Text,
		StickerURL: input.StickerURL,
		StickerID:  input.StickerID,
		ParentID:   input.ParentID,
		IsFirst:    isFirst,
	}

	if err := Config.DB.Create(&comment).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": false, "message": "Failed to post comment"})
		return
	}

	// Handle Mentions (@username)
	handleMentions(&comment)

	// Increment comment count on video
	Config.DB.Table("videos").Where("id = ?", videoID).UpdateColumn("comments", gorm.Expr("comments + 1"))

	// Increment sticker usage if applicable
	if comment.StickerID != nil {
		Config.DB.Table("stickers").Where("id = ?", *comment.StickerID).UpdateColumn("usage_count", gorm.Expr("usage_count + 1"))
	}

	// Reload with User info
	Config.DB.Preload("User").First(&comment, comment.ID)

	var updatedVideo struct {
		Comments int `json:"comments"`
	}
	Config.DB.Table("videos").Select("comments").Where("id = ?", videoID).First(&updatedVideo)

	c.JSON(http.StatusCreated, gin.H{
		"status":  true,
		"message": "Comment posted",
		"comment": comment,
	})

	// Broadcast update
	Realtime.MainHub.Broadcast(Realtime.BroadcastMessage{
		Type: "comment_update",
		Payload: gin.H{
			"video_id":      videoID,
			"comment_count": updatedVideo.Comments,
			"new_comment":   comment,
		},
	})

	// Notify video owner if it's not their own comment
	var v Home.Video
	Config.DB.Select("user_id").First(&v, videoID)
	if v.UserID != comment.UserID {
		go createNotificationHelper(v.UserID, &comment.UserID, "comment", &videoID, &comment.ID, "commented on your video")
	}
}

// ToggleCommentLike handles POST /api/v1/video/comment/:id/like
func ToggleCommentLike(c *gin.Context) {
	commentIDStr := c.Param("id")
	var commentID uint
	fmt.Sscanf(commentIDStr, "%d", &commentID)

	userIDRaw, _ := c.Get("userID")
	userID := userIDRaw.(uint)

	var existing CommentLike
	res := Config.DB.Where("user_id = ? AND comment_id = ?", userID, commentID).Limit(1).Find(&existing)

	liked := false
	Config.DB.Transaction(func(tx *gorm.DB) error {
		if res.RowsAffected > 0 {
			tx.Delete(&existing)
			tx.Model(&Comment{}).Where("id = ?", commentID).UpdateColumn("likes", gorm.Expr("likes - 1"))
			liked = false
		} else {
			tx.Create(&CommentLike{UserID: userID, CommentID: commentID})
			tx.Model(&Comment{}).Where("id = ?", commentID).UpdateColumn("likes", gorm.Expr("likes + 1"))
			liked = true
		}
		return nil
	})

	var comment Comment
	Config.DB.Select("likes").First(&comment, commentID)

	c.JSON(http.StatusOK, gin.H{
		"status":     true,
		"liked":      liked,
		"likes":      comment.Likes,
		"comment_id": commentID,
	})
}

// DeleteComment handles DELETE /api/v1/video/comment/:id
func DeleteComment(c *gin.Context) {
	commentID := c.Param("id")
	userIDRaw, _ := c.Get("userID")
	userID := userIDRaw.(uint)

	var comment Comment
	if err := Config.DB.First(&comment, commentID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"status": false, "message": "Comment not found"})
		return
	}

	// Permission: comment owner or video owner can delete
	var video Home.Video
	Config.DB.First(&video, comment.VideoID)

	if comment.UserID != userID && video.UserID != userID {
		c.JSON(http.StatusForbidden, gin.H{"status": false, "message": "Unauthorized to delete this comment"})
		return
	}

	if err := Config.DB.Delete(&comment).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": false, "message": "Failed to delete comment"})
		return
	}

	// Decrement comment count on video
	Config.DB.Table("videos").Where("id = ?", comment.VideoID).UpdateColumn("comments", gorm.Expr("comments - 1"))

	c.JSON(http.StatusOK, gin.H{"status": true, "message": "Comment deleted"})
}

// PinComment handles POST /api/v1/video/comment/:id/pin
func PinComment(c *gin.Context) {
	commentID := c.Param("id")
	userIDRaw, _ := c.Get("userID")
	userID := userIDRaw.(uint)

	var comment Comment
	if err := Config.DB.First(&comment, commentID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"status": false, "message": "Comment not found"})
		return
	}

	// Permission: Only video owner can pin
	var video Home.Video
	if err := Config.DB.First(&video, comment.VideoID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"status": false, "message": "Video not found"})
		return
	}

	if video.UserID != userID {
		c.JSON(http.StatusForbidden, gin.H{"status": false, "message": "Only the video owner can pin comments"})
		return
	}

	// Toggle Pinned
	newPinnedStatus := !comment.IsPinned

	err := Config.DB.Transaction(func(tx *gorm.DB) error {
		// If pinning, unpin any previously pinned comment on this video
		if newPinnedStatus {
			tx.Model(&Comment{}).Where("video_id = ? AND is_pinned = ?", comment.VideoID, true).Update("is_pinned", false)
		}

		return tx.Model(&comment).Update("is_pinned", newPinnedStatus).Error
	})

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": false, "message": "Failed to update pinned status"})
		return
	}

	if newPinnedStatus {
		c.JSON(http.StatusOK, gin.H{"status": true, "message": "Comment pinned", "is_pinned": newPinnedStatus})
	} else {
		c.JSON(http.StatusOK, gin.H{"status": true, "message": "Comment unpinned", "is_pinned": newPinnedStatus})
	}
}

const (
	voiceDir     = "public/uploads/audios"
	maxVoiceSize = 5 << 20 // 5MB
)

// PostVoiceComment handles POST /api/v1/video/:id/comment/voice
func PostVoiceComment(c *gin.Context) {
	videoIDStr := c.Param("id")
	userID, _ := c.Get("userID")

	file, err := c.FormFile("audio")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": false, "message": "Audio file required"})
		return
	}

	if file.Size > maxVoiceSize {
		c.JSON(http.StatusBadRequest, gin.H{"status": false, "message": "Audio file too large (max 5MB)"})
		return
	}

	var videoID uint
	fmt.Sscanf(videoIDStr, "%d", &videoID)

	durationStr := c.PostForm("duration")
	var duration int
	fmt.Sscanf(durationStr, "%d", &duration)

	parentIDStr := c.PostForm("parent_id")
	var parentID *uint
	if parentIDStr != "" {
		var pid uint
		fmt.Sscanf(parentIDStr, "%d", &pid)
		parentID = &pid
	}

	if err := os.MkdirAll(voiceDir, 0755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": false, "message": "Failed to create directory"})
		return
	}

	ext := filepath.Ext(file.Filename)
	if ext == "" {
		ext = ".m4a" // Default extension for record package
	}
	filename := fmt.Sprintf("voice_%d_%d%s", userID, time.Now().UnixNano(), ext)
	fullPath := filepath.Join(voiceDir, filename)

	if err := c.SaveUploadedFile(file, fullPath); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": false, "message": "Failed to save audio"})
		return
	}

	voiceURL := fmt.Sprintf("/uploads/audios/%s", filename)
	cldFolder := fmt.Sprintf("vx-app/users/%v/comments", userID)
	// Upload to Cloudinary
	if cldURL, err := Utils.UploadToCloudinary(fullPath, "video", cldFolder); err == nil && cldURL != "" {
		voiceURL = cldURL
		// Delete local file after successful Cloudinary upload
		os.Remove(fullPath)
	}

	// Check if this is the first top-level comment
	var count int64
	Config.DB.Model(&Comment{}).Where("video_id = ? AND parent_id IS NULL", videoID).Count(&count)
	isFirst := count == 0 && parentID == nil

	comment := Comment{
		VideoID:       videoID,
		UserID:        userID.(uint),
		IsVoice:       true,
		VoiceURL:      &voiceURL,
		VoiceDuration: duration,
		ParentID:      parentID,
		IsFirst:       isFirst,
		Text:          "Voice Comment", // Fallback text
	}

	if err := Config.DB.Create(&comment).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": false, "message": "Failed to create comment"})
		return
	}

	// Reload with User info
	Config.DB.Preload("User").First(&comment, comment.ID)

	// Increment comment count on video
	Config.DB.Table("videos").Where("id = ?", videoID).UpdateColumn("comments", gorm.Expr("comments + 1"))

	var updatedVideo struct {
		Comments int `json:"comments"`
	}
	Config.DB.Table("videos").Select("comments").Where("id = ?", videoID).First(&updatedVideo)

	c.JSON(http.StatusCreated, gin.H{
		"status":  true,
		"message": "Voice comment posted",
		"comment": comment,
	})

	// Broadcast update
	Realtime.MainHub.Broadcast(Realtime.BroadcastMessage{
		Type: "comment_update",
		Payload: gin.H{
			"video_id":      videoID,
			"comment_count": updatedVideo.Comments,
			"new_comment":   comment,
		},
	})

	// Notify video owner if it's not their own comment
	var v Home.Video
	Config.DB.Select("user_id").First(&v, videoID)
	if v.UserID != comment.UserID {
		go createNotificationHelper(v.UserID, &comment.UserID, "comment", &videoID, &comment.ID, "commented on your video (voice)")
	}
}

func handleMentions(comment *Comment) {
	if comment.Text == "" {
		return
	}

	re := regexp.MustCompile(`@([a-zA-Z0-9._]+)`)
	matches := re.FindAllStringSubmatch(comment.Text, -1)

	mentionedUsernames := make(map[string]bool)
	for _, match := range matches {
		if len(match) > 1 {
			mentionedUsernames[match[1]] = true
		}
	}

	for username := range mentionedUsernames {
		var user Profile.User
		if err := Config.DB.Where("username = ?", username).First(&user).Error; err == nil {
			// Trigger notification (Inbox system)
			// For now, let's just log. Inbox system will handle the actual broadcast.
			logMention(comment.UserID, user.ID, comment.VideoID, comment.ID)
		}
	}
}

func logMention(senderID, receiverID, videoID, commentID uint) {
	createNotificationHelper(receiverID, &senderID, "mention", &videoID, &commentID, "mentioned you in a comment")
}

func createNotificationHelper(receiverID uint, actorID *uint, nType string, videoID *uint, refID *uint, message string) {
	var content string
	if nType == "comment" && refID != nil {
		var comment struct{ Text string }
		Config.DB.Table("comments").Select("text").Where("id = ?", *refID).First(&comment)
		content = comment.Text
	}

	notification := map[string]interface{}{
		"user_id":      receiverID,
		"receiver_id":  receiverID,
		"actor_id":     actorID,
		"type":         nType,
		"video_id":     videoID,
		"reference_id": refID,
		"message":      message,
		"content":      content,
		"is_read":      false,
		"created_at":   time.Now(),
		"updated_at":   time.Now(),
	}
	Config.DB.Table("notifications").Create(&notification)

	var full map[string]interface{}
	Config.DB.Table("notifications").
		Select("notifications.*, users.nickname as actor_name, users.avatar_url as actor_avatar, users.username as actor_username, videos.thumbnail_url as video_thumbnail").
		Joins("left join users on users.id = notifications.actor_id").
		Joins("left join videos on videos.id = notifications.video_id").
		Where("notifications.id = ?", notification["id"]).
		First(&full)

	Realtime.MainHub.Broadcast(Realtime.BroadcastMessage{
		Type:    "notification",
		Payload: full,
		Target:  receiverID,
	})

	// Send FCM Push Notification
	var receiver Profile.User
	if err := Config.DB.Select("fcm_token").First(&receiver, receiverID).Error; err == nil && receiver.FCMToken != "" {
		title := "New Notification"
		if actorName, ok := full["actor_name"].(string); ok && actorName != "" {
			title = actorName
		}
		data := map[string]string{
			"type": nType,
		}
		if videoID != nil {
			data["video_id"] = fmt.Sprintf("%d", *videoID)
		}
		Utils.SendFCMNotification(receiver.FCMToken, title, message, data)
	}
}

func getUserIDOrZero(c *gin.Context) uint {
	if v, exists := c.Get("userID"); exists {
		if uid, ok := v.(uint); ok {
			return uid
		}
	}
	return 0
}

func enrichWithLikedStatus(userID uint, comments []Comment) {
	if userID == 0 || len(comments) == 0 {
		return
	}

	commentIDs := []uint{}
	for _, cm := range comments {
		commentIDs = append(commentIDs, cm.ID)
		for _, r := range cm.Replies {
			commentIDs = append(commentIDs, r.ID)
		}
	}

	var likes []CommentLike
	Config.DB.Where("user_id = ? AND comment_id IN ?", userID, commentIDs).Find(&likes)
	likedMap := map[uint]bool{}
	for _, l := range likes {
		likedMap[l.CommentID] = true
	}

	for i := range comments {
		comments[i].Liked = likedMap[comments[i].ID]
		for j := range comments[i].Replies {
			comments[i].Replies[j].Liked = likedMap[comments[i].Replies[j].ID]
		}
	}
}
