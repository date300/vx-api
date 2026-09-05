package Home

import (
	"time"
	"github.com/lib/pq"
	"gorm.io/gorm"
)

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
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
	DeletedAt    gorm.DeletedAt `gorm:"index" json:"-"`
}

// Category Model
type Category struct {
	ID        uint           `gorm:"primaryKey" json:"id"`
	Name      string         `gorm:"type:varchar(50);unique;not null" json:"name"`
	Slug      string         `gorm:"type:varchar(50);unique;not null" json:"slug"`
	CreatedAt time.Time      `json:"created_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
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

// Comment Model - Primary is in Comment package
type Comment struct {
	ID        uint           `gorm:"primaryKey" json:"id"`
	VideoID   uint           `gorm:"index;not null" json:"video_id"`
	UserID    uint           `gorm:"index;not null" json:"user_id"`
	User      User           `gorm:"foreignKey:UserID" json:"user"`
	Text      string         `gorm:"type:text;not null" json:"text"`
	ParentID  *uint          `gorm:"index" json:"parent_id"`
	Likes     int            `gorm:"default:0" json:"likes"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}

// Like Model
type Like struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	UserID    uint      `gorm:"not null;uniqueIndex:idx_user_video_like" json:"user_id"`
	VideoID   uint      `gorm:"not null;uniqueIndex:idx_user_video_like" json:"video_id"`
	CreatedAt time.Time `json:"created_at"`
}

// Save Model
type Save struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	UserID    uint      `gorm:"not null;uniqueIndex:idx_user_video_save" json:"user_id"`
	VideoID   uint      `gorm:"not null;uniqueIndex:idx_user_video_save" json:"video_id"`
	CreatedAt time.Time `json:"created_at"`
}

// CommentLike Model
type CommentLike struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	UserID    uint      `gorm:"not null;uniqueIndex:idx_user_comment_like" json:"user_id"`
	CommentID uint      `gorm:"not null;uniqueIndex:idx_user_comment_like" json:"comment_id"`
	CreatedAt time.Time `json:"created_at"`
}

type Follow struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	FollowerID  uint      `gorm:"not null;uniqueIndex:idx_follow_pair" json:"follower_id"`
	FollowingID uint      `gorm:"not null;uniqueIndex:idx_follow_pair" json:"following_id"`
	CreatedAt   time.Time `json:"created_at"`
}

func (Follow) TableName() string {
	return "follows"
}

// Interaction Model for recommendation signals
type Interaction struct {
	ID           uint      `gorm:"primaryKey" json:"id"`
	UserID       uint      `gorm:"index" json:"user_id"`
	VideoID      uint      `gorm:"index" json:"video_id"`
	Type         string    `gorm:"type:varchar(50);index" json:"type"` // WATCH, LIKE, COMMENT, SHARE, SAVE, FOLLOW, PROFILE_VISIT, SKIP, FAST_SCROLL, NOT_INTERESTED, REPORT
	WatchTime    float64   `gorm:"default:0" json:"watch_time"`
	IsCompletion bool      `gorm:"default:false" json:"is_completion"`
	IsRewatch    bool      `gorm:"default:false" json:"is_rewatch"`
	CreatedAt    time.Time `json:"created_at"`
}

// RecommendationInterest Model to track affinity to categories
type RecommendationInterest struct {
	ID         uint      `gorm:"primaryKey" json:"id"`
	UserID     uint      `gorm:"uniqueIndex:idx_user_reco_category" json:"user_id"`
	CategoryID uint      `gorm:"uniqueIndex:idx_user_reco_category" json:"category_id"`
	Score      float64   `gorm:"default:0" json:"score"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// Report Model
type Report struct {
	ID         uint      `gorm:"primaryKey" json:"id"`
	ReporterID uint      `gorm:"index;not null" json:"reporter_id"`
	TargetID   uint      `gorm:"index;not null" json:"target_id"` // UserID or VideoID
	TargetType string    `gorm:"type:varchar(20);index;not null" json:"target_type"` // "USER", "VIDEO", "COMMENT"
	Reason     string    `gorm:"type:text;not null" json:"reason"`
	Details    string    `gorm:"type:text" json:"details"`
	Status     string    `gorm:"type:varchar(20);default:'pending'" json:"status"` // "pending", "reviewed", "resolved", "dismissed"
	CreatedAt  time.Time `json:"created_at"`
}
