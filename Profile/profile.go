package Profile

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"vx-api/Config"
	"vx-api/Studio"
	"vx-api/Utils"

	"vx-api/Middleware"

	"github.com/gin-gonic/gin"
	"github.com/lib/pq"
	"gorm.io/gorm"
)

const (
	avatarDir     = "public/uploads/avatars"
	coverDir      = "public/uploads/covers"
	maxUploadSize = 8 << 20 // 8MB
)

// অনুমোদিত ইমেজ এক্সটেনশন — এর বাইরে কিছু আপলোড হবে না
var allowedImageExt = map[string]bool{
	".jpg":  true,
	".jpeg": true,
	".png":  true,
	".webp": true,
}

// RegisterRoutes handles actions related to user profiles and relationships
func RegisterRoutes(r *gin.RouterGroup) {
	// Root group: /user
	user := r.Group("/user")
	{
		// 1. Static Routes (Must be defined BEFORE wildcard routes)
		user.GET("/suggested", Middleware.AuthRequired(), GetSuggestedUsers)
		user.GET("/categories", Middleware.AuthRequired(), GetCategories)
		user.GET("/check-username", Middleware.AuthRequired(), CheckUsernameAvailability)
		user.POST("/onboard", Middleware.AuthRequired(), SaveOnboardingData)
		user.POST("/profile/avatar", Middleware.AuthRequired(), UploadAvatar)
		user.POST("/profile/cover", Middleware.AuthRequired(), UploadCover)
		user.POST("/profile/fcm-token", Middleware.AuthRequired(), SaveFCMToken)
		user.GET("/videos", Middleware.AuthRequired(), GetMyVideos)
		user.GET("/videos/liked", Middleware.AuthRequired(), GetLikedVideos)
		user.GET("/videos/saved", Middleware.AuthRequired(), GetSavedVideos)
		user.GET("/videos/commented", Middleware.AuthRequired(), GetCommentedVideos)

		// This is /api/v1/user/profile (OWN profile)
		user.GET("/profile", Middleware.AuthRequired(), GetOwnProfile)
		user.PUT("/profile", Middleware.AuthRequired(), UpdateOwnProfile)

		// 2. Wildcard Routes (MOST Generic - Keep at the bottom of the group)

		// Profile detail: /api/v1/user/:identifier/profile
		user.GET("/:identifier/profile", Middleware.OptionalAuth(), GetPublicProfile)

		// Public content: /api/v1/user/:identifier/videos
		user.GET("/:identifier/videos", GetUserVideos)
		user.GET("/:identifier/videos/liked", GetUserLikedVideos)

		// Relationships: /api/v1/user/:identifier/followers etc.
		user.GET("/:identifier/followers", Middleware.OptionalAuth(), GetFollowers)
		user.GET("/:identifier/following", Middleware.OptionalAuth(), GetFollowing)
		user.GET("/:identifier/friends", Middleware.OptionalAuth(), GetFriends)

		// Actions: /api/v1/user/follow/:identifier
		user.POST("/follow/:identifier", Middleware.AuthRequired(), FollowUser)
		user.DELETE("/follow/:identifier", Middleware.AuthRequired(), UnfollowUser)

		// Blocking: /api/v1/user/block/:identifier
		user.POST("/block/:identifier", Middleware.AuthRequired(), BlockUser)
		user.DELETE("/block/:identifier", Middleware.AuthRequired(), UnblockUser)
		user.GET("/blocked", Middleware.AuthRequired(), GetBlockedUsers)

		// Alternative/Legacy style to be safe
		user.GET("/p/:identifier", Middleware.OptionalAuth(), GetPublicProfile)
	}

	// Verification route
	r.GET("/ping-profile", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": true, "message": "Profile system is up"})
	})
}

// ========== Own Profile Logic ==========

func GetOwnProfile(c *gin.Context) {
	userID, exists := c.Get("userID")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"status": false, "message": "Unauthorized"})
		return
	}

	var user User
	if err := Config.DB.Preload("Interests").First(&user, userID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"status": false, "message": "User not found"})
		return
	}

	// Fetch active stories
	Config.DB.Where("user_id = ? AND status = ?", userID, "story").Order("created_at desc").Find(&user.Stories)

	c.JSON(http.StatusOK, gin.H{
		"status": true,
		"data":   user,
	})
}

// isUsernameTaken চেক করে username অন্য কোনো ইউজার আগে থেকে নিয়েছে কিনা
// excludeUserID = নিজের ID, নিজেরটার সাথে conflict হিসেবে গণনা না করার জন্য
func isUsernameTaken(username string, excludeUserID interface{}) (bool, error) {
	var count int64
	err := Config.DB.Model(&User{}).
		Where("username = ? AND id != ?", username, excludeUserID).
		Count(&count).Error
	return count > 0, err
}

func UpdateOwnProfile(c *gin.Context) {
	userID, exists := c.Get("userID")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"status": false, "message": "Unauthorized"})
		return
	}

	var req struct {
		Nickname     string `json:"nickname"`
		Username     string `json:"username"`
		Bio          string `json:"bio"`
		InstagramURL string `json:"instagram_url"`
		YoutubeURL   string `json:"youtube_url"`
		FacebookURL  string `json:"facebook_url"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": false, "message": "Invalid request"})
		return
	}

	var user User
	if err := Config.DB.First(&user, userID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"status": false, "message": "User not found"})
		return
	}

	if req.Username != "" {
		taken, err := isUsernameTaken(req.Username, userID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"status": false, "message": "Failed to validate username"})
			return
		}
		if taken {
			c.JSON(http.StatusConflict, gin.H{"status": false, "message": "এই ইউজারনেম আগে থেকেই নেওয়া হয়েছে"})
			return
		}
		user.Username = &req.Username
	}

	if req.Nickname != "" {
		user.Nickname = req.Nickname
	}
	if req.Bio != "" {
		user.Bio = req.Bio
	}
	if req.InstagramURL != "" {
		user.InstagramURL = req.InstagramURL
	}
	if req.YoutubeURL != "" {
		user.YoutubeURL = req.YoutubeURL
	}
	if req.FacebookURL != "" {
		user.FacebookURL = req.FacebookURL
	}

	if err := Config.DB.Save(&user).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": false, "message": "Failed to update profile"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": true, "message": "Profile updated", "data": user})
}

func GetCategories(c *gin.Context) {
	var categories []Category
	if err := Config.DB.Find(&categories).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": false, "message": "Failed to fetch categories"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": true, "data": categories})
}

func CheckUsernameAvailability(c *gin.Context) {
	username := c.Query("username")
	if username == "" {
		c.JSON(http.StatusBadRequest, gin.H{"status": false, "message": "Username is required"})
		return
	}

	userID, _ := c.Get("userID")
	taken, err := isUsernameTaken(username, userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": false, "message": "Database error"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": true, "available": !taken})
}

func SaveOnboardingData(c *gin.Context) {
	userID, exists := c.Get("userID")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"status": false, "message": "Unauthorized"})
		return
	}

	var req struct {
		Nickname  string `json:"nickname"`
		Username  string `json:"username"`
		Interests []uint `json:"interests"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": false, "message": "Invalid request"})
		return
	}

	if req.Username != "" {
		taken, err := isUsernameTaken(req.Username, userID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"status": false, "message": "Failed to validate username"})
			return
		}
		if taken {
			c.JSON(http.StatusConflict, gin.H{"status": false, "message": "এই ইউজারনেম আগে থেকেই নেওয়া হয়েছে"})
			return
		}
	}

	var user User

	// পুরো onboarding প্রসেসটা একটা transaction-এ — মাঝপথে কোনো ধাপ ফেইল করলে
	// পুরোটাই rollback হয়ে যাবে, interests অর্ধেক অবস্থায় সেভ হবে না
	err := Config.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.First(&user, userID).Error; err != nil {
			return err
		}

		if err := tx.Model(&user).Association("Interests").Clear(); err != nil {
			return err
		}

		if len(req.Interests) > 0 {
			var categories []Category
			if err := tx.Where("id IN ?", req.Interests).Find(&categories).Error; err != nil {
				return err
			}
			if err := tx.Model(&user).Association("Interests").Append(&categories); err != nil {
				return err
			}
		}

		if req.Nickname != "" {
			user.Nickname = req.Nickname
		}
		if req.Username != "" {
			user.Username = &req.Username
		}
		user.IsOnboarded = true

		return tx.Save(&user).Error
	})

	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"status": false, "message": "User not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"status": false, "message": "Failed to save onboarding data"})
		return
	}

	// Interests সহ আপডেটেড ইউজার রিটার্ন করা
	Config.DB.Preload("Interests").First(&user, userID)

	c.JSON(http.StatusOK, gin.H{"status": true, "message": "Onboarding saved", "data": user})
}

// ========== Media Upload Logic ==========

// saveUploadedImage ফাইল টাইপ/সাইজ ভ্যালিডেট করে ডিস্কে সেভ করে, পাবলিক URL রিটার্ন করে
func saveUploadedImage(c *gin.Context, baseDir string, prefix string, userID uint) (string, error) {
	file, err := c.FormFile("file")
	if err != nil {
		return "", fmt.Errorf("no file provided: %w", err)
	}

	if file.Size > maxUploadSize {
		return "", fmt.Errorf("file too large (max %dMB)", maxUploadSize/(1<<20))
	}

	ext := strings.ToLower(filepath.Ext(file.Filename))
	if !allowedImageExt[ext] {
		return "", fmt.Errorf("unsupported file type: only jpg, jpeg, png, webp allowed")
	}

	// শুধু extension না, ফাইলের আসল content দেখেও যাচাই করা (magic bytes)
	opened, err := file.Open()
	if err != nil {
		return "", fmt.Errorf("failed to read file")
	}
	defer opened.Close()

	buf := make([]byte, 512)
	n, _ := opened.Read(buf)
	contentType := http.DetectContentType(buf[:n])
	if !strings.HasPrefix(contentType, "image/") {
		return "", fmt.Errorf("file content is not a valid image")
	}

	// User ID অনুযায়ী পাথ তৈরি
	userDir := filepath.Join("public/uploads/users", strconv.Itoa(int(userID)), baseDir)
	if err := os.MkdirAll(userDir, 0755); err != nil {
		return "", err
	}

	filename := fmt.Sprintf("%s_%d%s", prefix, time.Now().UnixNano(), ext)
	fullPath := filepath.Join(userDir, filename)

	if err := c.SaveUploadedFile(file, fullPath); err != nil {
		return "", err
	}

	// পাবলিক লোকাল URL (যদি Cloudinary না থাকে)
	// লোকাল পাথকে URL-এ কনভার্ট করার সময় উইন্ডোজ/লিনাক্স স্ল্যাশ ঠিক রাখা
	webPath := filepath.ToSlash(fullPath)
	publicURL := fmt.Sprintf("%s/%s", strings.TrimSuffix(Config.BaseURL, "/"), webPath)

	// Cloudinary ফোল্ডার পাথ
	cldFolder := fmt.Sprintf("vx-app/users/%d/profiles", userID)

	// Upload to Cloudinary
	if cldURL, err := Utils.UploadToCloudinary(fullPath, "image", cldFolder); err == nil && cldURL != "" {
		publicURL = cldURL
		// Delete local file after successful Cloudinary upload
		os.Remove(fullPath)
	}

	return publicURL, nil
}

// deleteOldImageFile পুরনো avatar/cover ফাইল ডিস্ক থেকে মুছে দেয়।
// oldURL খালি হলে বা লোকাল ফাইল না হলে (যেমন কোনো ডিফল্ট/এক্সটার্নাল URL) কিছু করে না।
func deleteOldImageFile(oldURL string) {
	if oldURL == "" {
		return
	}
	base := strings.TrimSuffix(Config.BaseURL, "/") + "/"
	if !strings.HasPrefix(oldURL, base) {
		return // এক্সটার্নাল/ডিফল্ট URL, ডিলিট করার দরকার নেই
	}
	relativePath := strings.TrimPrefix(oldURL, base)
	// path traversal protection
	cleanPath := filepath.Clean(relativePath)
	if strings.Contains(cleanPath, "..") {
		return
	}
	if err := os.Remove(cleanPath); err != nil && !os.IsNotExist(err) {
		fmt.Printf("Warning: failed to delete old file %s: %v\n", cleanPath, err)
	}
}

func UploadAvatar(c *gin.Context) {
	userIDRaw, _ := c.Get("userID")
	userID := userIDRaw.(uint)

	var user User
	if err := Config.DB.Select("avatar_url").First(&user, userID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"status": false, "message": "User not found"})
		return
	}
	oldAvatarURL := user.AvatarURL

	url, err := saveUploadedImage(c, "avatars", "avatar", userID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": false, "message": err.Error()})
		return
	}

	if err := Config.DB.Model(&User{}).Where("id = ?", userID).Update("avatar_url", url).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": false, "message": "Failed to save avatar"})
		return
	}

	deleteOldImageFile(oldAvatarURL)

	c.JSON(http.StatusOK, gin.H{"status": true, "message": "Avatar updated", "data": gin.H{"avatar_url": url}})
}

func UploadCover(c *gin.Context) {
	userIDRaw, _ := c.Get("userID")
	userID := userIDRaw.(uint)

	var user User
	if err := Config.DB.Select("cover_url").First(&user, userID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"status": false, "message": "User not found"})
		return
	}
	oldCoverURL := user.CoverURL

	url, err := saveUploadedImage(c, "covers", "cover", userID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": false, "message": err.Error()})
		return
	}

	if err := Config.DB.Model(&User{}).Where("id = ?", userID).Update("cover_url", url).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": false, "message": "Failed to save cover"})
		return
	}

	deleteOldImageFile(oldCoverURL)

	c.JSON(http.StatusOK, gin.H{"status": true, "message": "Cover updated", "data": gin.H{"cover_url": url}})
}

func SaveFCMToken(c *gin.Context) {
	userID, _ := c.Get("userID")

	var req struct {
		Token string `json:"token" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": false, "message": "Token is required"})
		return
	}

	if err := Config.DB.Model(&User{}).Where("id = ?", userID).Update("fcm_token", req.Token).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": false, "message": "Failed to save FCM token"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": true, "message": "FCM token saved"})
}

func GetMyVideos(c *gin.Context) {
	userID := getUserIDOrZero(c)

	var videos []Video
	if err := Config.DB.Preload("User").Where("user_id = ?", userID).Order("created_at desc").Find(&videos).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": false, "message": "Failed to load videos"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": true, "data": enrichVideos(userID, videos)})
}

func GetLikedVideos(c *gin.Context) {
	userID := getUserIDOrZero(c)

	var videos []Video
	err := Config.DB.Table("videos").
		Select("videos.*, likes.created_at as interacted_at").
		Joins("JOIN likes ON likes.video_id = videos.id").
		Where("likes.user_id = ?", userID).
		Preload("User").
		Order("likes.created_at desc").
		Find(&videos).Error

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": false, "message": "Failed to load liked videos"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": true, "data": enrichVideos(userID, videos)})
}

func GetSavedVideos(c *gin.Context) {
	userID := getUserIDOrZero(c)

	var videos []Video
	err := Config.DB.Table("videos").
		Select("videos.*, saves.created_at as interacted_at").
		Joins("JOIN saves ON saves.video_id = videos.id").
		Where("saves.user_id = ?", userID).
		Preload("User").
		Order("saves.created_at desc").
		Find(&videos).Error

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": false, "message": "Failed to load saved videos"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": true, "data": enrichVideos(userID, videos)})
}

func GetCommentedVideos(c *gin.Context) {
	userID, _ := c.Get("userID")

	var comments []Comment
	// Fetch user's comments, preloading the video and video uploader
	err := Config.DB.Preload("Video").Preload("Video.User").
		Where("user_id = ?", userID).
		Order("created_at desc").
		Find(&comments).Error

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": false, "message": "Failed to load comment history"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": true, "data": comments})
}



// User Model
type User struct {
	ID           uint           `gorm:"primaryKey" json:"id"`
	Email        string         `gorm:"type:varchar(100);unique;not null" json:"email"`
	Provider     string         `gorm:"type:varchar(20);not null" json:"provider"`
	Nickname     string         `gorm:"type:varchar(100)" json:"nickname"`
	Username     *string        `gorm:"type:varchar(50);unique" json:"username"`
	IsOnboarded  bool           `gorm:"default:false" json:"is_onboarded"`
	Bio          string         `gorm:"type:text" json:"bio"`
	AvatarURL    string         `gorm:"type:text" json:"avatar_url"`
	CoverURL     string         `gorm:"type:text" json:"cover_url"`
	Following    int            `gorm:"default:0" json:"following"`
	Followers    int            `gorm:"default:0" json:"followers"`
	Likes        int            `gorm:"default:0" json:"likes"`
	InstagramURL string         `gorm:"type:text" json:"instagram_url"`
	YoutubeURL   string         `gorm:"type:text" json:"youtube_url"`
	FacebookURL  string         `gorm:"type:text" json:"facebook_url"`
	FCMToken     string         `gorm:"type:text" json:"fcm_token"`
	IsVerified   bool           `gorm:"default:false" json:"is_verified"`
	IsAdmin      bool           `gorm:"default:false" json:"is_admin"`
	IsBanned     bool           `gorm:"default:false" json:"is_banned"`
	IsOnline     bool           `gorm:"default:false" json:"is_online"`
	LastSeen     time.Time      `json:"last_seen"`
	RefreshToken string         `gorm:"type:text" json:"-"`
	OTPCode      string         `gorm:"type:varchar(6)" json:"-"`
	OTPExpiresAt *time.Time     `gorm:"index" json:"-"`
	Interests    []Category     `gorm:"many2many:user_interests;" json:"interests"`
	Stories      []Video      `gorm:"-" json:"stories"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
	DeletedAt    gorm.DeletedAt `gorm:"index" json:"-"`
}

// Video Model
type Video struct {
	ID              uint           `gorm:"primaryKey" json:"id"`
	UserID          uint           `gorm:"index;not null" json:"user_id"`
	User            User           `gorm:"foreignKey:UserID" json:"user"`
	URL             string         `gorm:"type:text;not null" json:"url"`
	Caption         string         `gorm:"type:text" json:"caption"`
	Duration        int            `gorm:"column:duration" json:"duration"`
	ThumbnailURL    string         `gorm:"column:thumbnail_url" json:"thumbnail_url"`
	Status          string         `gorm:"column:status;default:'processing'" json:"status"`
	Views           int64          `gorm:"default:0" json:"views"`
	SoundID         *int64         `gorm:"column:sound_id;default:null" json:"sound_id"`
	Sound           string         `gorm:"type:varchar(100)" json:"sound"`
	OriginalVideoID *uint          `gorm:"index" json:"original_video_id"`
	Likes           int            `gorm:"default:0" json:"likes"`
	Comments        int            `gorm:"default:0" json:"comments"`
	Shares          int            `gorm:"default:0" json:"shares"`
	RankingScore    float64        `gorm:"default:0" json:"ranking_score"`
	Bucket          int            `gorm:"default:0" json:"bucket"`
	BucketViews     int            `gorm:"default:0" json:"bucket_views"`
	EngagementScore float64        `gorm:"default:0" json:"engagement_score"`
	CategoryID      *uint          `gorm:"index" json:"category_id"`
	IsImage         bool           `gorm:"default:false" json:"is_image"`
	Images          pq.StringArray `gorm:"type:text[]" json:"images"`
	IsAd            bool           `gorm:"default:false" json:"is_ad"`
	AdCta           string         `gorm:"type:varchar(50)" json:"ad_cta"`
	AdLink          string         `gorm:"type:text" json:"ad_link"`
	Filter          string         `gorm:"type:varchar(50)" json:"filter"`
	Speed           float64        `gorm:"type:decimal(3,2);default:1.0" json:"speed"`
	AllowComments   bool           `gorm:"default:true" json:"allow_comments"`
	AllowDuet       bool           `gorm:"default:true" json:"allow_duet"`
	AllowSave       bool           `gorm:"default:true" json:"allow_save"`
	IsPublic        bool           `gorm:"default:true" json:"is_public"`
	Location        string         `gorm:"type:varchar(255)" json:"location"`
	TextOverlays    string         `gorm:"type:text" json:"text_overlays"`
	InteractedAt    time.Time      `gorm:"column:interacted_at;->" json:"interacted_at"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
	DeletedAt       gorm.DeletedAt `gorm:"index" json:"-"`
}

// Category Model
type Category struct {
	ID        uint           `gorm:"primaryKey" json:"id"`
	Name      string         `gorm:"type:varchar(50);unique;not null" json:"name"`
	Slug      string         `gorm:"type:varchar(50);unique;not null" json:"slug"`
	CreatedAt time.Time      `json:"created_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}

type Comment struct {
	ID        uint           `gorm:"primaryKey" json:"id"`
	VideoID   uint           `gorm:"index;not null" json:"video_id"`
	Video     Video          `gorm:"foreignKey:VideoID" json:"video"`
	UserID    uint           `gorm:"index;not null" json:"user_id"`
	User      User           `gorm:"foreignKey:UserID" json:"user"`
	Text      string         `gorm:"type:text;not null" json:"text"`
	Likes     int            `gorm:"default:0" json:"likes"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}

func (Comment) TableName() string {
	return "comments"
}

// ========== Consolidated Public Profile Logic ==========

func formatLastActive(t time.Time) string {
	if t.IsZero() {
		return "long ago"
	}
	diff := time.Since(t)
	if diff.Minutes() < 1 {
		return "Just now"
	}
	if diff.Minutes() < 60 {
		return fmt.Sprintf("%d min ago", int(diff.Minutes()))
	}
	if diff.Hours() < 24 {
		return fmt.Sprintf("%d hours ago", int(diff.Hours()))
	}
	return t.Format("02 Jan")
}

func GetPublicProfile(c *gin.Context) {
	myID, _ := c.Get("userID")
	identifier := strings.TrimSpace(c.Param("identifier"))

	if identifier == "" {
		c.JSON(http.StatusBadRequest, gin.H{"status": false, "message": "Identifier required"})
		return
	}

	fmt.Printf("DEBUG: GetPublicProfile for identifier: %s\n", identifier)

	target, err := resolveUserByIdentifier(identifier)
	if err != nil {
		fmt.Printf("DEBUG: User not found for identifier: %s, error: %v\n", identifier, err)
		c.JSON(http.StatusNotFound, gin.H{"status": false, "message": "User not found: " + identifier})
		return
	}

	myIDUint, _ := myID.(uint)
	isSelf := target.ID == myIDUint

	var isFollowing, isFollowedBy bool
	if !isSelf && myIDUint > 0 {
		var f Follow
		isFollowing = Config.DB.Where("follower_id = ? AND following_id = ?", myIDUint, target.ID).Limit(1).Find(&f).RowsAffected > 0

		var fb Follow
		isFollowedBy = Config.DB.Where("follower_id = ? AND following_id = ?", target.ID, myIDUint).Limit(1).Find(&fb).RowsAffected > 0

		// Analytics: Increment profile visit for target user
		Studio.IncrementDailyStat(target.ID, "profile_visits", 1)
	}

	// Fetch active stories
	var stories []Video
	Config.DB.Where("user_id = ? AND status = ?", target.ID, "story").Order("created_at desc").Find(&stories)

	c.JSON(http.StatusOK, gin.H{
		"status": true,
		"data": gin.H{
			"id":             target.ID,
			"nickname":       target.Nickname,
			"username":       target.Username,
			"bio":            target.Bio,
			"avatar_url":     target.AvatarURL,
			"cover_url":      target.CoverURL,
			"following":      target.Following,
			"followers":      target.Followers,
			"likes":          target.Likes,
			"instagram_url":  target.InstagramURL,
			"youtube_url":    target.YoutubeURL,
			"facebook_url":   target.FacebookURL,
			"is_verified":    target.IsVerified,
			"is_admin":       target.IsAdmin,
			"is_onboarded":   target.IsOnboarded,
			"is_following":   isFollowing,
			"is_followed_by": isFollowedBy,
			"is_self":        isSelf,
			"is_online":      target.IsOnline,
			"last_active":    formatLastActive(target.LastSeen),
			"stories":        stories,
		},
	})
}

func GetUserVideos(c *gin.Context) {
	userID := getUserIDOrZero(c)
	identifier := strings.TrimSpace(c.Param("identifier"))

	target, err := resolveUserByIdentifier(identifier)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"status": false, "message": "User not found: " + identifier})
		return
	}

	var videos []Video
	Config.DB.Preload("User").Where("user_id = ?", target.ID).Order("created_at desc").Find(&videos)
	c.JSON(http.StatusOK, gin.H{"status": true, "data": enrichVideos(userID, videos)})
}

func GetUserLikedVideos(c *gin.Context) {
	userID := getUserIDOrZero(c)
	identifier := strings.TrimSpace(c.Param("identifier"))

	target, err := resolveUserByIdentifier(identifier)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"status": false, "message": "User not found: " + identifier})
		return
	}

	var videos []Video
	err = Config.DB.Table("videos").
		Select("videos.*").
		Joins("JOIN likes ON likes.video_id = videos.id").
		Where("likes.user_id = ?", target.ID).
		Preload("User").
		Order("likes.created_at desc").
		Find(&videos).Error

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": false, "message": "Failed to load liked videos"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": true, "data": enrichVideos(userID, videos)})
}

func FollowUser(c *gin.Context) {
	myID, exists := c.Get("userID")
	myIDUint, ok := myID.(uint)
	if !exists || !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"status": false, "message": "Unauthorized"})
		return
	}
	identifier := strings.TrimSpace(c.Param("identifier"))

	target, err := resolveUserByIdentifier(identifier)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"status": false, "message": "User not found: " + identifier})
		return
	}

	if target.ID == myIDUint {
		c.JSON(http.StatusBadRequest, gin.H{"status": false, "message": "Cannot follow yourself"})
		return
	}

	// Check if already following to prevent duplicate key errors
	var existing Follow
	if err := Config.DB.Where("follower_id = ? AND following_id = ?", myIDUint, target.ID).First(&existing).Error; err == nil {
		c.JSON(http.StatusOK, gin.H{"status": true, "message": "Already followed"})
		return
	}

	txErr := Config.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&Follow{FollowerID: myIDUint, FollowingID: target.ID}).Error; err != nil {
			return err
		}
		tx.Model(&User{}).Where("id = ?", target.ID).UpdateColumn("followers", gorm.Expr("followers + 1"))
		tx.Model(&User{}).Where("id = ?", myIDUint).UpdateColumn("following", gorm.Expr("following + 1"))

		// Analytics: Increment follower count for target user
		Studio.IncrementDailyStat(target.ID, "followers", 1)

		return nil
	})

	if txErr != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": false, "message": "Failed to follow"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": true, "message": "Followed"})
}

func UnfollowUser(c *gin.Context) {
	myID, exists := c.Get("userID")
	myIDUint, ok := myID.(uint)
	if !exists || !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"status": false, "message": "Unauthorized"})
		return
	}
	identifier := strings.TrimSpace(c.Param("identifier"))

	target, err := resolveUserByIdentifier(identifier)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"status": false, "message": "User not found: " + identifier})
		return
	}

	txErr := Config.DB.Transaction(func(tx *gorm.DB) error {
		result := tx.Delete(&Follow{}, "follower_id = ? AND following_id = ?", myIDUint, target.ID)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			// Wasn't following in the first place — nothing to decrement
			return nil
		}
		tx.Model(&User{}).Where("id = ? AND followers > 0", target.ID).UpdateColumn("followers", gorm.Expr("followers - 1"))
		tx.Model(&User{}).Where("id = ? AND following > 0", myIDUint).UpdateColumn("following", gorm.Expr("following - 1"))
		return nil
	})

	if txErr != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": false, "message": "Failed to unfollow"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": true, "message": "Unfollowed"})
}

// resolveUserByIdentifier helper to resolve user by ID or Username
// resolveUserByIdentifier helper to resolve user by ID or Username
func resolveUserByIdentifier(identifier string) (*User, error) {
	var user User
	identifier = strings.TrimSpace(identifier)
	if identifier == "" {
		return nil, errors.New("empty identifier")
	}

	fmt.Printf("DEBUG: resolveUserByIdentifier searching for: '%s'\n", identifier)

	// Strip leading @ if present for username search
	cleanIdentifier := strings.TrimPrefix(identifier, "@")

	// Try numeric ID first (only if it doesn't start with @ and is purely numeric)
	if !strings.HasPrefix(identifier, "@") {
		if id, err := strconv.ParseUint(identifier, 10, 64); err == nil {
			// Use Table("users") and First to find by Primary Key or ID
			if err := Config.DB.Table("users").Where("id = ?", uint(id)).First(&user).Error; err == nil {
				fmt.Printf("DEBUG: Found user by ID: %d\n", user.ID)
				return &user, nil
			}
		}
	}

	// Fallback to username search if ID search failed or identifier was not numeric or had @
	if err := Config.DB.Table("users").Where("username = ?", cleanIdentifier).First(&user).Error; err != nil {
		fmt.Printf("DEBUG: User NOT found by username: '%s', error: %v\n", cleanIdentifier, err)
		return nil, err
	}

	usernameStr := "unknown"
	if user.Username != nil {
		usernameStr = *user.Username
	}
	fmt.Printf("DEBUG: Found user by username: '%s' (ID: %d)\n", usernameStr, user.ID)
	return &user, nil
}


// Follow Table
type Follow struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	FollowerID  uint      `gorm:"not null;uniqueIndex:idx_follow_pair" json:"follower_id"`
	FollowingID uint      `gorm:"not null;uniqueIndex:idx_follow_pair" json:"following_id"`
	CreatedAt   time.Time `json:"created_at"`
}

func (Follow) TableName() string {
	return "follows"
}

func getUserIDOrZero(c *gin.Context) uint {
	if v, exists := c.Get("userID"); exists {
		if uid, ok := v.(uint); ok {
			return uid
		}
	}
	return 0
}

func enrichVideos(userID uint, videos []Video) []gin.H {
	result := make([]gin.H, 0, len(videos))
	if len(videos) == 0 {
		return result
	}

	likedVideoIDs := map[uint]bool{}
	savedVideoIDs := map[uint]bool{}
	followingIDs := map[uint]bool{}
	followedByIDs := map[uint]bool{}

	if userID != 0 {
		videoIDs := make([]uint, 0, len(videos))
		uploaderIDs := make([]uint, 0, len(videos))
		for _, v := range videos {
			videoIDs = append(videoIDs, v.ID)
			uploaderIDs = append(uploaderIDs, v.UserID)
		}

		var likes []struct{ VideoID uint }
		Config.DB.Table("likes").Where("user_id = ? AND video_id IN ?", userID, videoIDs).Find(&likes)
		for _, l := range likes {
			likedVideoIDs[l.VideoID] = true
		}

		var saves []struct{ VideoID uint }
		Config.DB.Table("saves").Where("user_id = ? AND video_id IN ?", userID, videoIDs).Find(&saves)
		for _, s := range saves {
			savedVideoIDs[s.VideoID] = true
		}

		var following []struct{ FollowingID uint }
		Config.DB.Table("follows").Where("follower_id = ? AND following_id IN ?", userID, uploaderIDs).Find(&following)
		for _, f := range following {
			followingIDs[f.FollowingID] = true
		}

		var followedBy []struct{ FollowerID uint }
		Config.DB.Table("follows").Where("following_id = ? AND follower_id IN ?", userID, uploaderIDs).Find(&followedBy)
		for _, f := range followedBy {
			followedByIDs[f.FollowerID] = true
		}
	}

	for _, v := range videos {
		// Filter out videos with missing physical files
		if !strings.HasPrefix(v.URL, "http") && !Utils.FileExists(v.URL) {
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
			Config.DB.Table("sounds").Where("id = ?", *v.SoundID).Limit(1).Find(&s)
			if s.ID != 0 {
				soundData = s
			}
		}

		var interactedAt interface{}
		if !v.InteractedAt.IsZero() {
			interactedAt = v.InteractedAt
		}

		result = append(result, gin.H{
			"id":             v.ID,
			"user_id":        v.UserID,
			"user":           v.User,
			"url":            v.URL,
			"caption":        v.Caption,
			"duration":       v.Duration,
			"thumbnail_url":  v.ThumbnailURL,
			"status":         v.Status,
			"views":          v.Views,
			"likes":          v.Likes,
			"comments":       v.Comments,
			"shares":          v.Shares,
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
			"sound":          v.Sound,
			"sound_id":       v.SoundID,
			"sound_data":     soundData,
			"interacted_at":  interactedAt,
			"created_at":     v.CreatedAt,
			"is_liked":       likedVideoIDs[v.ID],
			"is_saved":       savedVideoIDs[v.ID],
			"is_following":   followingIDs[v.UserID],
			"is_followed_by": followedByIDs[v.UserID],
		})
	}
	return result
}

// ========== Follow List Logic (Consolidated) ==========

func GetFollowers(c *gin.Context) {
	identifier := strings.TrimSpace(c.Param("identifier"))
	user, err := resolveUserByIdentifier(identifier)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"status": false, "message": "User not found"})
		return
	}

	var followers []User
	Config.DB.Table("users").
		Joins("join follows on follows.follower_id = users.id").
		Where("follows.following_id = ?", user.ID).
		Select("users.*").
		Find(&followers)

	myID := getUserIDOrZero(c)
	c.JSON(http.StatusOK, gin.H{"status": true, "data": enrichUsers(myID, followers)})
}

func GetFollowing(c *gin.Context) {
	identifier := strings.TrimSpace(c.Param("identifier"))
	user, err := resolveUserByIdentifier(identifier)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"status": false, "message": "User not found"})
		return
	}

	var following []User
	Config.DB.Table("users").
		Joins("join follows on follows.following_id = users.id").
		Where("follows.follower_id = ?", user.ID).
		Select("users.*").
		Find(&following)

	myID := getUserIDOrZero(c)
	c.JSON(http.StatusOK, gin.H{"status": true, "data": enrichUsers(myID, following)})
}

func GetFriends(c *gin.Context) {
	identifier := strings.TrimSpace(c.Param("identifier"))
	user, err := resolveUserByIdentifier(identifier)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"status": false, "message": "User not found"})
		return
	}

	var friends []User
	// Mutual followers logic
	query := `
		SELECT u.* FROM users u
		JOIN follows f1 ON f1.following_id = u.id
		JOIN follows f2 ON f2.follower_id = u.id
		WHERE f1.follower_id = ? AND f2.following_id = ?
	`
	Config.DB.Raw(query, user.ID, user.ID).Scan(&friends)

	myID := getUserIDOrZero(c)
	c.JSON(http.StatusOK, gin.H{"status": true, "data": enrichUsers(myID, friends)})
}

func GetSuggestedUsers(c *gin.Context) {
	userID, _ := c.Get("userID")
	uid, _ := userID.(uint)

	var suggested []User
	Config.DB.Raw(`
		SELECT * FROM users
		WHERE id != ?
		AND id NOT IN (SELECT following_id FROM follows WHERE follower_id = ?)
		ORDER BY followers DESC
		LIMIT 20
	`, uid, uid).Scan(&suggested)

	c.JSON(http.StatusOK, gin.H{"status": true, "data": enrichUsers(uid, suggested)})
}

func enrichUsers(myID uint, users []User) []gin.H {
	result := make([]gin.H, 0, len(users))
	if len(users) == 0 {
		return result
	}

	followingIDs := map[uint]bool{}
	followedByIDs := map[uint]bool{}

	if myID != 0 {
		userIDs := make([]uint, 0, len(users))
		for _, u := range users {
			userIDs = append(userIDs, u.ID)
		}

		var following []Follow
		Config.DB.Where("follower_id = ? AND following_id IN ?", myID, userIDs).Find(&following)
		for _, f := range following {
			followingIDs[f.FollowingID] = true
		}

		var followedBy []Follow
		Config.DB.Where("follower_id IN ? AND following_id = ?", userIDs, myID).Find(&followedBy)
		for _, f := range followedBy {
			followedByIDs[f.FollowerID] = true
		}
	}

	for _, u := range users {
		result = append(result, gin.H{
			"id":             u.ID,
			"username":       u.Username,
			"nickname":       u.Nickname,
			"avatar_url":     u.AvatarURL,
			"is_verified":    u.IsVerified,
			"is_following":   followingIDs[u.ID],
			"is_followed_by": followedByIDs[u.ID],
			"is_self":        myID == u.ID,
		})
	}
	return result
}

// ========== Block Logic ==========

func BlockUser(c *gin.Context) {
	myID, _ := c.Get("userID")
	myIDUint, _ := myID.(uint)
	identifier := strings.TrimSpace(c.Param("identifier"))

	target, err := resolveUserByIdentifier(identifier)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"status": false, "message": "User not found"})
		return
	}

	if target.ID == myIDUint {
		c.JSON(http.StatusBadRequest, gin.H{"status": false, "message": "Cannot block yourself"})
		return
	}

	// Unfollow both ways if blocking
	Config.DB.Delete(&Follow{}, "(follower_id = ? AND following_id = ?) OR (follower_id = ? AND following_id = ?)", myIDUint, target.ID, target.ID, myIDUint)

	// Create block entry
	block := Block{BlockerID: myIDUint, BlockedID: target.ID}
	if err := Config.DB.FirstOrCreate(&block, Block{BlockerID: myIDUint, BlockedID: target.ID}).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": false, "message": "Failed to block user"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": true, "message": "User blocked"})
}

func UnblockUser(c *gin.Context) {
	myID, _ := c.Get("userID")
	myIDUint, _ := myID.(uint)
	identifier := strings.TrimSpace(c.Param("identifier"))

	target, err := resolveUserByIdentifier(identifier)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"status": false, "message": "User not found"})
		return
	}

	if err := Config.DB.Delete(&Block{}, "blocker_id = ? AND blocked_id = ?", myIDUint, target.ID).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": false, "message": "Failed to unblock user"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": true, "message": "User unblocked"})
}

func GetBlockedUsers(c *gin.Context) {
	myID, _ := c.Get("userID")
	myIDUint, _ := myID.(uint)

	var blockedUsers []User
	Config.DB.Table("users").
		Joins("join blocks on blocks.blocked_id = users.id").
		Where("blocks.blocker_id = ?", myIDUint).
		Select("users.*").
		Find(&blockedUsers)

	c.JSON(http.StatusOK, gin.H{"status": true, "data": enrichUsers(myIDUint, blockedUsers)})
}

// Block Table
type Block struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	BlockerID uint      `gorm:"not null;uniqueIndex:idx_block_pair" json:"blocker_id"`
	BlockedID uint      `gorm:"not null;uniqueIndex:idx_block_pair" json:"blocked_id"`
	CreatedAt time.Time `json:"created_at"`
}

func (Block) TableName() string {
	return "blocks"
}
