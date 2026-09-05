package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"vx-api/Auth"
	"vx-api/Config"
	"vx-api/Explore"
	"vx-api/Home"
	"vx-api/Home/Comment"
	"vx-api/Home/Sticker"
	"vx-api/Inbox"
	"vx-api/Middleware"
	"vx-api/Profile"
	"vx-api/Realtime"
	"vx-api/Settings"
	"vx-api/Studio"
	"vx-api/Upload"
	"vx-api/Utils"
	"vx-api/Algorithms"
	"vx-api/Admin"

	"github.com/gin-gonic/gin"
)

func main() {
	fmt.Println("MAIN: Start")
	Config.InitDB()
	fmt.Println("MAIN: DB Init Done")
	Config.InitRedis()
	fmt.Println("MAIN: Redis Init Done")
	Explore.SeedSounds()
	fmt.Println("MAIN: Seed Sounds Done")
	Utils.InitFirebase()
	fmt.Println("MAIN: Firebase Init Done")

	// Start Background Ranking Worker
	go func() {
		ticker := time.NewTicker(30 * time.Minute)
		for range ticker.C {
			log.Println("Updating global ranking scores...")
			if err := Algorithms.UpdateGlobalRankingScores(Config.DB); err != nil {
				log.Printf("Failed to update ranking scores: %v", err)
			}
		}
	}()

	// Ensure essential tables are migrated
	
	// Auto-Migrate all models
	Config.DB.AutoMigrate(
		&Profile.User{},
		&Profile.Category{},
		&Profile.Video{},
		&Comment.Comment{},
		&Home.Like{},
		&Comment.CommentLike{},
		&Sticker.Sticker{},
		&Sticker.SavedSticker{},
		&Home.Save{},
		&Home.Interaction{},
		&Home.RecommendationInterest{},
		&Profile.Follow{},
		&Profile.Block{},
		&Inbox.Notification{},
		&Inbox.Conversation{},
		&Inbox.Message{},
		&Explore.Hashtag{},
		&Explore.Sound{},
		&Explore.VideoHashtag{},
		&Settings.UserSettings{},
		&Home.Report{},
		&Inbox.MessageReaction{},
		&Inbox.PinnedMessage{},
		&Inbox.CallHistory{},
		&Studio.DailyStats{},
	)


	if Config.AppEnv == "production" {
		gin.SetMode(gin.ReleaseMode)
	} else {
		gin.SetMode(gin.DebugMode)
	}

	r := gin.Default()
	r.MaxMultipartMemory = 100 << 20 // 100 MiB

	// Enable CORS
	r.Use(Middleware.CORSMiddleware())

	// Initialize Realtime Hub
	go Realtime.MainHub.Run()

	// Consolidated Static Routes with custom logging/headers to fix 403/404 issues
	// We handle /uploads and /public separately to avoid route wildcard conflicts

	serveStatic := func(root string) gin.HandlerFunc {
		return func(c *gin.Context) {
			path := c.Param("filepath")
			// Remove any leading slashes that might cause double slashes or join issues
			path = strings.TrimPrefix(path, "/")
			fullPath := filepath.Join(root, path)

			// Broad permissions for media streaming (avoiding duplicate Access-Control-Allow-Origin)
			c.Writer.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS, HEAD")
			c.Writer.Header().Set("Access-Control-Expose-Headers", "Content-Length, Content-Range")
			c.Writer.Header().Set("Cross-Origin-Resource-Policy", "cross-origin")

			// Set proper HLS MIME types
			if strings.HasSuffix(path, ".m3u8") {
				c.Writer.Header().Set("Content-Type", "application/x-mpegURL")
			} else if strings.HasSuffix(path, ".ts") {
				c.Writer.Header().Set("Content-Type", "video/mp2t")
			} else if strings.HasSuffix(path, ".mp4") {
				c.Writer.Header().Set("Content-Type", "video/mp4")
			} else if strings.HasSuffix(path, ".webp") {
				c.Writer.Header().Set("Content-Type", "image/webp")
			}

			// Log the access to debug 403/404
			log.Printf("DEBUG: Static Access: %s -> %s | UA: %s | Range: %s",
				c.Request.URL.Path, fullPath, c.Request.UserAgent(), c.Request.Header.Get("Range"))

			if _, err := os.Stat(fullPath); os.IsNotExist(err) {
				c.JSON(http.StatusNotFound, gin.H{"error": "File not found"})
				return
			}

			c.File(fullPath)
		}
	}

	// Route definitions - No overlap to prevent Gin panic
	r.Match([]string{"GET", "HEAD"}, "/uploads/*filepath", serveStatic("./public/uploads"))
	r.Match([]string{"GET", "HEAD"}, "/public/*filepath", serveStatic("./public"))

	// Global API Group - Top level /api
	api := r.Group("/api/v1")
	{
		// Module routers
		Auth.RegisterRoutes(api)
		Home.RegisterRoutes(api)
		Comment.RegisterRoutes(api)
		Sticker.RegisterRoutes(api)
		Profile.RegisterRoutes(api)    // Profile & Follow actions (Consolidated)
		Explore.RegisterRoutes(api)    // Explore & Search
		Inbox.RegisterRoutes(api)      // Inbox & Notifications
		Settings.RegisterRoutes(api)   // User Settings
		Studio.RegisterRoutes(api)     // Creator Studio
		Upload.RegisterRoutes(api)
		Realtime.RegisterRoutes(api)
		Admin.RegisterRoutes(api)

		// Explicit debug route to test if DELETE works at all
		api.DELETE("/test-delete/:id", func(c *gin.Context) {
			c.JSON(200, gin.H{"status": true, "message": "Delete reached", "id": c.Param("id")})
		})
	}

	fmt.Printf("VX API Engine Running on %s:%s...\n", Config.ServerHost, Config.ServerPort)

	server := &http.Server{
		Addr:           Config.ServerHost + ":" + Config.ServerPort,
		Handler:        r,
		ReadTimeout:    60 * time.Second,
		WriteTimeout:   60 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}

	if err := server.ListenAndServe(); err != nil {
		panic(err)
	}
}
