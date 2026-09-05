package Upload

import (
	"time"
	"gorm.io/gorm"
	"vx-api/Home"
	"vx-api/Explore"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"vx-api/Config"
	"vx-api/Realtime"
	"vx-api/Utils"
	"vx-api/Middleware"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/lib/pq"
)

// RegisterRoutes handles all Upload related endpoints
func RegisterRoutes(r *gin.RouterGroup) {
	uploadGroup := r.Group("/upload", Middleware.AuthRequired())
	{
		uploadGroup.POST("/video", Upload)
	}
}

// Upload handles POST /api/v1/upload/video
func Upload(c *gin.Context) {
	rawID, exists := c.Get("userID")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var uid uint
	switch v := rawID.(type) {
	case float64:
		uid = uint(v)
	case int:
		uid = uint(v)
	case int64:
		uid = uint(v)
	case uint:
		uid = v
	case uint64:
		uid = uint(v)
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "invalid user id"})
		return
	}

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 100<<20)
	if err := c.Request.ParseMultipartForm(10 << 20); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "file too large or bad request"})
		return
	}

	isImage := c.PostForm("is_image") == "true"
	isStory := c.PostForm("is_story") == "true"
	caption := c.PostForm("caption")
	soundIDStr := c.PostForm("sound_id")
	hashtagsStr := c.PostForm("hashtags")
	coverTimestamp := c.PostForm("cover_timestamp")
	filter := c.PostForm("filter")
	speedStr := c.PostForm("speed")
	textOverlays := c.PostForm("text_overlays")
	allowComments := c.PostForm("allow_comments") != "false"
	allowDuet := c.PostForm("allow_duet") != "false"
	allowSave := c.PostForm("allow_save") != "false"
	isPublic := c.PostForm("is_public") != "false"
	location := c.PostForm("location")

	// Profanity Filter for Caption
	if !Utils.IsContentSafe(caption) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "caption contains forbidden or offensive language"})
		return
	}

	uploadDir := Config.UploadDir
	if uploadDir == "" {
		uploadDir = "./public/uploads"
	}
	// User ID অনুযায়ী বেস ডিরেক্টরি তৈরি
	userBaseDir := filepath.Join(uploadDir, "users", strconv.Itoa(int(uid)))
	cldFolder := fmt.Sprintf("vx-app/users/%d", uid)

	var videoURL string
	var thumbURL string
	var imageURLs []string

	if isImage {
		// Handle multiple images if provided
		files := c.Request.MultipartForm.File["images"]
		// Fallback to "video" field if "images" is empty (for single image backward compatibility)
		if len(files) == 0 {
			if f, h, err := c.Request.FormFile("video"); err == nil {
				defer f.Close()
				files = append(files, h)
			}
		}

		if len(files) == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "no images provided"})
			return
		}

		for _, header := range files {
			ext := strings.ToLower(filepath.Ext(header.Filename))
			if ext != ".jpg" && ext != ".jpeg" && ext != ".png" && ext != ".webp" {
				continue // Skip unsupported formats
			}

			imgUUID := uuid.New().String()
			imgFileName := imgUUID + ext
			imgPath := filepath.Join(userBaseDir, "images", imgFileName)

			if err := os.MkdirAll(filepath.Dir(imgPath), 0755); err != nil {
				continue
			}

			// BUG FIX: previously used `defer src.Close()` / `defer dst.Close()`
			// inside this loop. Deferred calls only run when the *function*
			// returns, not each loop iteration — with many images this leaked
			// open file descriptors until the whole request finished. We now
			// close each file explicitly at the end of the iteration via a
			// closure so resources are released immediately.
			func() {
				src, err := header.Open()
				if err != nil {
					return
				}
				defer src.Close()

				dst, err := os.Create(imgPath)
				if err != nil {
					return
				}
				defer dst.Close()

				if _, err := io.Copy(dst, src); err != nil {
					return
				}

				url := "/uploads/users/" + strconv.Itoa(int(uid)) + "/images/" + imgFileName
				// Upload to Cloudinary
				if cldURL, err := Utils.UploadToCloudinary(imgPath, "image", cldFolder+"/images"); err == nil && cldURL != "" {
					url = cldURL
					// Delete local file after successful Cloudinary upload
					os.Remove(imgPath)
				}

				imageURLs = append(imageURLs, url)
			}()
		}

		if len(imageURLs) == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "no valid images uploaded"})
			return
		}

		videoURL = imageURLs[0]
		thumbURL = imageURLs[0]
	} else {
		// Handle single video
		file, header, err := c.Request.FormFile("video")
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "no video file provided"})
			return
		}
		defer file.Close()

		ext := strings.ToLower(filepath.Ext(header.Filename))
		isAudio := ext == ".mp3" || ext == ".wav" || ext == ".m4a" || ext == ".aac" || ext == ".ogg"
		if !isAudio && ext != ".mp4" && ext != ".mov" && ext != ".avi" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported video or audio format"})
			return
		}

		videoUUID := uuid.New().String()
		videoFileName := videoUUID + ext
		videoPath := filepath.Join(userBaseDir, "videos", videoFileName)

		if err := os.MkdirAll(filepath.Dir(videoPath), 0755); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "server error"})
			return
		}

		dst, err := os.Create(videoPath)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "server error"})
			return
		}
		defer dst.Close()

		if _, err := io.Copy(dst, file); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "server error"})
			return
		}

		videoURL = "/uploads/users/" + strconv.Itoa(int(uid)) + "/videos/" + videoFileName
		// Upload to Cloudinary
		if cldURL, err := Utils.UploadToCloudinary(videoPath, "video", cldFolder+"/videos"); err == nil && cldURL != "" {
			videoURL = cldURL
		}

		if coverTimestamp == "" {
			coverTimestamp = "00:00:01"
		}

		// Initial thumbnail URL (will be updated by background worker)
		thumbURL = "/uploads/users/" + strconv.Itoa(int(uid)) + "/thumbnails/" + videoUUID + ".jpg"
		if isAudio {
			thumbURL = "" // No local thumbnail for audio
		}

		// Background Processing (Thumbnail only, no HLS)
		go func(vPath, vURL, vUUID, ts string, userFolder string, userID uint, baseDir string, story bool, audio bool) {
			// BUG FIX: previously did a single lookup after a fixed 1s sleep.
			// If DB insert was slow (load, network latency, etc.) the video
			// row wasn't found yet and the worker silently gave up, leaving
			// the video without a thumbnail and stuck in "processing"
			// forever. Now we retry a few times with backoff.
			var vid uint
			for attempt := 0; attempt < 5; attempt++ {
				time.Sleep(1 * time.Second)
				Config.DB.Table("videos").Where("url = ?", vURL).Select("id").Scan(&vid)
				if vid != 0 {
					break
				}
			}
			if vid == 0 {
				log.Printf("Background worker: Video record not found for %s after retries", vURL)
				return
			}

			// 1. Generate Thumbnail
			var thumbnailPath string
			if !audio {
				thumbnailPath = generateThumbnail(vPath, baseDir, vUUID, ts)
			}

			if thumbnailPath != "" {
				// Upload thumbnail to Cloudinary
				finalThumbURL := "/uploads/users/" + strconv.Itoa(int(userID)) + "/thumbnails/" + filepath.Base(thumbnailPath)
				if cldThumbURL, err := Utils.UploadToCloudinary(thumbnailPath, "image", userFolder+"/thumbnails"); err == nil && cldThumbURL != "" {
					finalThumbURL = cldThumbURL
				}
				Config.DB.Table("videos").Where("id = ?", vid).Update("thumbnail_url", finalThumbURL)
			}

			// 2. Set final status.
			// BUG FIX: previously this always set status to "ready", even for
			// stories — overwriting the "story" status that was set at
			// creation time. Stories should stay marked as "story" so the
			// rest of the app (feed, expiry logic, etc.) can still tell them
			// apart from normal ready videos.
			finalStatus := "ready"
			if story {
				finalStatus = "story"
			}
			Config.DB.Table("videos").Where("id = ?", vid).Update("status", finalStatus)

			// 3. Delete local files after processing
			os.Remove(vPath)
			if thumbnailPath != "" {
				os.Remove(thumbnailPath)
			}

			// Broadcast update when ready
			go func() {
				var fullVideo Video
				if err := Config.DB.Preload("User").First(&fullVideo, vid).Error; err == nil {
					payload := gin.H{
						"id":     fullVideo.ID,
						"url":    fullVideo.URL,
						"status": finalStatus,
						"thumbnail_url": fullVideo.ThumbnailURL,
					}
					Realtime.MainHub.Broadcast(Realtime.BroadcastMessage{
						Type:    "video_ready",
						Payload: payload,
					})
				}
			}()
		}(videoPath, videoURL, videoUUID, coverTimestamp, cldFolder, uid, userBaseDir, isStory, isAudio)
	}

	var soundID *int64
	if soundIDStr != "" {
		if id, err := strconv.ParseInt(soundIDStr, 10, 64); err == nil {
			soundID = &id
		}
	}

	var speed float64
	if speedStr != "" {
		speed, _ = strconv.ParseFloat(speedStr, 64)
	}

	video := Video{
		UserID:       uid,
		URL:          videoURL,
		Caption:      caption,
		Duration:     0,
		ThumbnailURL: thumbURL,
		Status:       "ready",
		IsImage:      isImage,
		Images:       pq.StringArray(imageURLs),
		Filter:       filter,
		Speed:        speed,
		TextOverlays: textOverlays,
		AllowComments: allowComments,
		AllowDuet:    allowDuet,
		AllowSave:    allowSave,
		IsPublic:     isPublic,
		Location:     location,
	}
	if !isImage {
		video.Status = "processing"
	}
	if isStory {
		video.Status = "story"
	}

	if soundID != nil {
		video.SoundID = soundID
	}

	if err := Config.DB.Create(&video).Error; err != nil {
		log.Printf("db insert error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "server error"})
		return
	}

	// Sound Logic: If no sound_id, create original sound
	if video.SoundID == nil {
		var u User
		// BUG FIX: error from this lookup was previously ignored, so if the
		// user record couldn't be found (e.g. deleted mid-request), `u`
		// stayed as a zero-value struct and code silently continued with an
		// empty nickname/avatar instead of surfacing the problem.
		if err := Config.DB.First(&u, uid).Error; err != nil {
			log.Printf("could not load user %d for sound creation: %v", uid, err)
		}

		nickname := u.Nickname
		if nickname == "" {
			nickname = "Vx User"
		}

		newSound := Explore.Sound{
			Title:           "Original Sound - " + nickname,
			AuthorName:      nickname,
			AuthorAvatar:    thumbURL,
			AudioURL:        videoURL, // Original sound uses the video's audio
			OriginalVideoID: &video.ID,
			TotalVideos:     1,
		}

		if err := Config.DB.Create(&newSound).Error; err == nil {
			sid := int64(newSound.ID)
			Config.DB.Model(&video).Update("sound_id", sid)
		}
	} else {
		// সাউন্ড ব্যবহার করা হলে টোটাল ভিডিও সংখ্যা বাড়ানো
		Config.DB.Table("sounds").Where("id = ?", *video.SoundID).UpdateColumn("total_videos", gorm.Expr("total_videos + 1"))
	}

	if hashtagsStr != "" {
		tags := strings.Split(hashtagsStr, ",")
		for _, tag := range tags {
			tag = strings.TrimSpace(tag)
			if tag == "" {
				continue
			}
			var hashtag Hashtag
			res := Config.DB.Where("name = ?", tag).FirstOrCreate(&hashtag, Hashtag{Name: tag})
			if res.Error != nil {
				log.Printf("hashtag error: %v", res.Error)
				continue
			}
			if err := Config.DB.Create(&VideoHashtag{
				VideoID:   video.ID,
				HashtagID: hashtag.ID,
			}).Error; err != nil {
				// BUG FIX: previously this error was silently swallowed
				// (not even logged), which made duplicate-tag or FK issues
				// invisible during debugging.
				log.Printf("video_hashtag error: %v", err)
			}
		}
	}

	c.JSON(http.StatusCreated, gin.H{
		"id":            video.ID,
		"url":           videoURL,
		"thumbnail_url": thumbURL,
	})

	// Broadcast the new video to all connected clients
	go func() {
		var fullVideo Video
		if err := Config.DB.Preload("User").First(&fullVideo, video.ID).Error; err == nil {
			// Construct payload similar to what Home.enrichVideos does
			payload := gin.H{
				"id":            fullVideo.ID,
				"user_id":       fullVideo.UserID,
				"user":          fullVideo.User,
				"url":           fullVideo.URL,
				"caption":       fullVideo.Caption,
				"sound":         fullVideo.Sound,
				"sound_id":      fullVideo.SoundID,
				"duration":      fullVideo.Duration,
				"thumbnail_url": fullVideo.ThumbnailURL,
				"status":        fullVideo.Status,
				"likes":         fullVideo.Likes,
				"comments":      fullVideo.Comments,
				"views":         fullVideo.Views,
				"shares":        fullVideo.Shares,
				"is_image":      fullVideo.IsImage,
				"images":        fullVideo.Images,
				"is_ad":         fullVideo.IsAd,
				"filter":        fullVideo.Filter,
				"speed":         fullVideo.Speed,
				"text_overlays": fullVideo.TextOverlays,
				"created_at":    fullVideo.CreatedAt,
				"is_liked":      false,
				"is_saved":      false,
				"is_following":  false,
			}
			Realtime.MainHub.Broadcast(Realtime.BroadcastMessage{
				Type:    "new_video",
				Payload: payload,
			})
		}
	}()
}

func generateThumbnail(videoPath, uploadDir, videoUUID, timestamp string) string {
	thumbDir := filepath.Join(uploadDir, "thumbnails")
	os.MkdirAll(thumbDir, 0755)
	thumbPath := filepath.Join(thumbDir, videoUUID+".jpg")

	cmd := exec.Command("ffmpeg",
		"-i", videoPath,
		"-ss", timestamp,
		"-vframes", "1",
		"-q:v", "2",
		thumbPath,
	)
	if err := cmd.Run(); err != nil {
		log.Printf("ffmpeg thumbnail error: %v", err)
		return ""
	}
	return thumbPath
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