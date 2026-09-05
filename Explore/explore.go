package Explore

import (
	"gorm.io/gorm"
	"vx-api/Home"
	"net/http"
	"strconv"
	"strings"
	"time"

	"vx-api/Config"
	
	"vx-api/Middleware"

	"github.com/gin-gonic/gin"
	"github.com/lib/pq"
)

const (
	defaultPageSize = 20
	maxPageSize     = 50
)

// RegisterRoutes handles all Explore and Search related endpoints
func RegisterRoutes(r *gin.RouterGroup) {
	exploreGroup := r.Group("/explore")
	{
		// OptionalAuth যাতে লগইন করা ইউজার হলে is_liked/is_following দেখানো যায়,
		// guest হলেও সার্চ/ট্রেন্ডিং কাজ করবে
		exploreGroup.GET("/search", Middleware.OptionalAuth(), SearchVideos)
		exploreGroup.GET("/search/all", Middleware.OptionalAuth(), SearchAll)
		exploreGroup.GET("/users", Middleware.OptionalAuth(), SearchUsers)
		exploreGroup.GET("/trending", Middleware.OptionalAuth(), GetTrending)
		exploreGroup.GET("/hashtags/search", SearchHashtags)
		UpdateSoundRoutes(exploreGroup)
	}
}

// SearchHashtags handles GET /api/v1/explore/hashtags/search?q=query
func SearchHashtags(c *gin.Context) {
	query := strings.TrimSpace(c.Query("q"))
	limit, offset := parsePagination(c)

	var hashtags []Hashtag
	var total int64

	dbQuery := Config.DB.Model(&Hashtag{})

	if query != "" {
		searchTerm := "%" + escapeLike(strings.ToLower(query)) + "%"
		dbQuery = dbQuery.Where("LOWER(name) LIKE ? ESCAPE '\\'", searchTerm)
	}

	dbQuery.Count(&total)

	if err := dbQuery.Order("total_videos desc").Limit(limit).Offset(offset).Find(&hashtags).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": false, "message": "Hashtag fetch failed"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status": true,
		"query":  query,
		"total":  total,
		"limit":  limit,
		"offset": offset,
		"data":   hashtags,
	})
}

// escapeLike LIKE প্যাটার্নে ব্যবহারকারীর ইনপুটে থাকা %, _ কে literal হিসেবে treat করার জন্য escape করে
// (নাহলে ইউজার '%' বা '_' লিখলে unintended wildcard match হয়ে যায়)
func escapeLike(input string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return replacer.Replace(input)
}

func parsePagination(c *gin.Context) (limit, offset int) {
	limit = defaultPageSize
	if l, err := strconv.Atoi(c.Query("limit")); err == nil && l > 0 && l <= maxPageSize {
		limit = l
	}
	offset = 0
	if o, err := strconv.Atoi(c.Query("offset")); err == nil && o >= 0 {
		offset = o
	}
	return
}

// SearchVideos handles GET /api/v1/explore/search?q=query&limit=20&offset=0
// caption, uploader-এর username/nickname, ও hashtag — এই তিন জায়গায় সার্চ করে
func SearchVideos(c *gin.Context) {
	query := strings.TrimSpace(c.Query("q"))
	if query == "" {
		c.JSON(http.StatusBadRequest, gin.H{"status": false, "message": "Search query is required"})
		return
	}

	limit, offset := parsePagination(c)
	searchTerm := "%" + escapeLike(strings.ToLower(query)) + "%"

	// LOWER(...) LIKE ... ব্যবহার করা হয়েছে ILIKE-এর বদলে, যাতে Postgres ছাড়াও
	// MySQL/SQLite-এ একই কোড কাজ করে (ILIKE Postgres-specific)
	dbQuery := Config.DB.Model(&Video{}).
		Distinct("videos.*").
		Joins("LEFT JOIN users ON users.id = videos.user_id").
		Joins("LEFT JOIN video_hashtags ON video_hashtags.video_id = videos.id").
		Joins("LEFT JOIN hashtags ON hashtags.id = video_hashtags.hashtag_id").
		Where(
			"LOWER(videos.caption) LIKE ? ESCAPE '\\' OR LOWER(users.username) LIKE ? ESCAPE '\\' OR LOWER(users.nickname) LIKE ? ESCAPE '\\' OR LOWER(hashtags.name) LIKE ? ESCAPE '\\'",
			searchTerm, searchTerm, searchTerm, searchTerm,
		)

	var total int64
	dbQuery.Count(&total)

	var videos []Video
	if err := dbQuery.Preload("User").
		Order("videos.created_at desc").
		Limit(limit).Offset(offset).
		Find(&videos).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": false, "message": "Search failed"})
		return
	}

	userID := getUserIDOrZero(c)

	c.JSON(http.StatusOK, gin.H{
		"status": true,
		"query":  query,
		"total":  total,
		"limit":  limit,
		"offset": offset,
		"data":   enrichVideos(userID, videos),
	})
}

// SearchUsers handles GET /api/v1/explore/users?q=query&limit=20&offset=0
// username এবং nickname-এ সার্চ করে
func SearchUsers(c *gin.Context) {
	query := strings.TrimSpace(c.Query("q"))
	if query == "" {
		c.JSON(http.StatusBadRequest, gin.H{"status": false, "message": "Search query is required"})
		return
	}

	limit, offset := parsePagination(c)
	searchTerm := "%" + escapeLike(strings.ToLower(query)) + "%"

	var total int64
	Config.DB.Model(&User{}).
		Where("LOWER(username) LIKE ? ESCAPE '\\' OR LOWER(nickname) LIKE ? ESCAPE '\\'", searchTerm, searchTerm).
		Count(&total)

	var users []User
	if err := Config.DB.
		Where("LOWER(username) LIKE ? ESCAPE '\\' OR LOWER(nickname) LIKE ? ESCAPE '\\'", searchTerm, searchTerm).
		Order("followers desc").
		Limit(limit).Offset(offset).
		Find(&users).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": false, "message": "User search failed"})
		return
	}

	userID := getUserIDOrZero(c)

	c.JSON(http.StatusOK, gin.H{
		"status": true,
		"query":  query,
		"total":  total,
		"limit":  limit,
		"offset": offset,
		"data":   enrichUsers(userID, users),
	})
}

// GetTrending handles GET /api/v1/explore/trending
// "Trending" এখন গত ৭ দিনের ভিডিওগুলোর মধ্যে সবচেয়ে বেশি engagement (likes+comments+views)
// পাওয়া ভিডিও দেখায়। এই উইন্ডোতে যথেষ্ট ভিডিও না পাওয়া গেলে all-time জনপ্রিয় ভিডিও দিয়ে পূরণ করে।
func GetTrending(c *gin.Context) {
	const trendingWindow = 7 * 24 * time.Hour
	const trendingLimit = 10

	since := time.Now().Add(-trendingWindow)

	var recentVideos []Video
	Config.DB.Preload("User").
		Where("created_at >= ?", since).
		Order("(likes::bigint * 3 + comments::bigint * 2 + views::bigint) desc").
		Limit(trendingLimit).
		Find(&recentVideos)

	videos := recentVideos
	if len(videos) < trendingLimit {
		// সাম্প্রতিক উইন্ডোতে যথেষ্ট ভিডিও না থাকলে all-time জনপ্রিয় ভিডিও দিয়ে বাকিটা পূরণ
		excludeIDs := make([]uint, 0, len(videos))
		for _, v := range videos {
			excludeIDs = append(excludeIDs, v.ID)
		}

		remaining := trendingLimit - len(videos)
		var fallbackVideos []Video
		fallbackQuery := Config.DB.Preload("User").Order("likes desc").Limit(remaining)
		if len(excludeIDs) > 0 {
			fallbackQuery = fallbackQuery.Where("id NOT IN ?", excludeIDs)
		}
		fallbackQuery.Find(&fallbackVideos)
		videos = append(videos, fallbackVideos...)
	}

	var hashtags []Hashtag
	Config.DB.Order("total_videos desc").Limit(5).Find(&hashtags)

	userID := getUserIDOrZero(c)

	c.JSON(http.StatusOK, gin.H{
		"status":   true,
		"videos":   enrichVideos(userID, videos),
		"hashtags": hashtags,
	})
}

// getUserIDOrZero OptionalAuth middleware থেকে userID বের করে, না থাকলে 0 রিটার্ন করে
func getUserIDOrZero(c *gin.Context) uint {
	if v, exists := c.Get("userID"); exists {
		if uid, ok := v.(uint); ok {
			return uid
		}
	}
	return 0
}

// enrichUsers adds is_following status to user list
func enrichUsers(userID uint, users []User) []gin.H {
	result := make([]gin.H, 0, len(users))
	if len(users) == 0 {
		return result
	}

	followingIDs := map[uint]bool{}
	if userID != 0 {
		userIDs := make([]uint, 0, len(users))
		for _, u := range users {
			userIDs = append(userIDs, u.ID)
		}

		var following []Home.Follow
		Config.DB.Where("follower_id = ? AND following_id IN ?", userID, userIDs).Find(&following)
		for _, f := range following {
			followingIDs[f.FollowingID] = true
		}
	}

	for _, u := range users {
		result = append(result, gin.H{
			"id":           u.ID,
			"username":     u.Username,
			"nickname":     u.Nickname,
			"avatar_url":   u.AvatarURL,
			"followers":    u.Followers,
			"following":    u.Following,
			"is_verified":  u.IsVerified,
			"is_following": followingIDs[u.ID],
		})
	}
	return result
}

// enrichVideos ভিডিও লিস্টে is_liked, is_following, is_follow_back যোগ করে
func enrichVideos(userID uint, videos []Video) []gin.H {
	result := make([]gin.H, 0, len(videos))
	if len(videos) == 0 {
		return result
	}

	likedVideoIDs := map[uint]bool{}
	followingIDs := map[uint]bool{}
	followBackIDs := map[uint]bool{}
	savedVideoIDs := map[uint]bool{}

	if userID != 0 {
		videoIDs := make([]uint, 0, len(videos))
		uploaderIDs := make([]uint, 0, len(videos))
		for _, v := range videos {
			videoIDs = append(videoIDs, v.ID)
			uploaderIDs = append(uploaderIDs, v.UserID)
		}

		var likes []Home.Like
		Config.DB.Where("user_id = ? AND video_id IN ?", userID, videoIDs).Find(&likes)
		for _, l := range likes {
			likedVideoIDs[l.VideoID] = true
		}

		var following []Home.Follow
		Config.DB.Where("follower_id = ? AND following_id IN ?", userID, uploaderIDs).Find(&following)
		for _, f := range following {
			followingIDs[f.FollowingID] = true
		}

		var followBack []Home.Follow
		Config.DB.Where("follower_id IN ? AND following_id = ?", uploaderIDs, userID).Find(&followBack)
		for _, f := range followBack {
			followBackIDs[f.FollowerID] = true
		}

		var saves []Home.Save
		Config.DB.Where("user_id = ? AND video_id IN ?", userID, videoIDs).Find(&saves)
		for _, s := range saves {
			savedVideoIDs[s.VideoID] = true
		}
	}

	for _, v := range videos {
		// মিউজিক ডিটেইলস খুঁজে বের করা যদি থাকে
		var soundData *Sound
		if v.SoundID != nil {
			var s Sound
			if err := Config.DB.First(&s, *v.SoundID).Error; err == nil {
				soundData = &s
			}
		}

		result = append(result, gin.H{
			"id":             v.ID,
			"user_id":        v.UserID,
			"user":           v.User,
			"url":            v.URL,
			"thumbnail_url":  v.ThumbnailURL,
			"caption":        v.Caption,
			"sound":          v.Sound,
			"sound_id":       v.SoundID,
			"sound_data":     soundData,
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
			"created_at":     v.CreatedAt,
			"is_liked":       likedVideoIDs[v.ID],
			"is_saved":       savedVideoIDs[v.ID],
			"is_following":   followingIDs[v.UserID],
			"is_follow_back": followBackIDs[v.UserID],
		})
	}
	return result
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
	IsVerified   bool           `gorm:"default:false" json:"is_verified"`
	RefreshToken string         `gorm:"type:text" json:"-"`
	OTPCode      string         `gorm:"type:varchar(6)" json:"-"`
	OTPExpiresAt *time.Time     `gorm:"index" json:"-"`
	Interests    []Home.Category     `gorm:"many2many:user_interests;" json:"interests"`
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

// Hashtag Model
type Hashtag struct {
	ID          uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	Name        string    `gorm:"uniqueIndex;not null" json:"name"`
	TotalVideos int       `gorm:"column:total_videos;default:0" json:"total_videos"`
	TotalViews  int64     `gorm:"column:total_views;default:0" json:"total_views"`
	CreatedAt   time.Time `json:"created_at"`
}

type VideoHashtag struct {
	ID        uint `gorm:"primaryKey;autoIncrement" json:"id"`
	VideoID   uint `gorm:"column:video_id;uniqueIndex:idx_video_hashtag" json:"video_id"`
	HashtagID uint `gorm:"column:hashtag_id;uniqueIndex:idx_video_hashtag" json:"hashtag_id"`
}

// SearchAll combines videos, users, and sounds into a single professional search result
func SearchAll(c *gin.Context) {
	query := strings.TrimSpace(c.Query("q"))
	if query == "" {
		c.JSON(http.StatusBadRequest, gin.H{"status": false, "message": "Search query is required"})
		return
	}

	searchTerm := "%" + escapeLike(strings.ToLower(query)) + "%"
	userID := getUserIDOrZero(c)

	// 1. Search Users (Top 5)
	var users []User
	Config.DB.Where("LOWER(username) LIKE ? ESCAPE '\\' OR LOWER(nickname) LIKE ? ESCAPE '\\'", searchTerm, searchTerm).
		Order("followers desc").Limit(5).Find(&users)

	// 2. Search Videos (Top 12)
	var videos []Video
	Config.DB.Model(&Video{}).
		Distinct("videos.*").
		Joins("LEFT JOIN users ON users.id = videos.user_id").
		Where("LOWER(videos.caption) LIKE ? ESCAPE '\\' OR LOWER(users.username) LIKE ? ESCAPE '\\' OR LOWER(users.nickname) LIKE ? ESCAPE '\\'", searchTerm, searchTerm, searchTerm).
		Preload("User").Order("videos.created_at desc").Limit(12).Find(&videos)

	// 3. Search Sounds (Top 5)
	var sounds []Sound
	Config.DB.Where("LOWER(title) LIKE ? ESCAPE '\\' OR LOWER(author_name) LIKE ? ESCAPE '\\'", searchTerm, searchTerm).
		Limit(5).Find(&sounds)

	c.JSON(http.StatusOK, gin.H{
		"status": true,
		"data": gin.H{
			"users":  enrichUsers(userID, users),
			"videos": enrichVideos(userID, videos),
			"sounds": sounds,
		},
	})
}
