package Home

import (
	"vx-api/Middleware"
	"github.com/gin-gonic/gin"
)

// RegisterRoutes handles all Home related endpoints
func RegisterRoutes(r *gin.RouterGroup) {
	homeGroup := r.Group("/home")
	{
		// Auth optional to allow public viewing and silent state for guests
		homeGroup.GET("/foryou", Middleware.OptionalAuth(), GetForYouVideos)
		homeGroup.GET("/following", Middleware.OptionalAuth(), GetFollowingVideos)
		homeGroup.GET("/friends", Middleware.OptionalAuth(), GetFriendsVideos)
		homeGroup.GET("/stories", Middleware.OptionalAuth(), GetStories)
		homeGroup.POST("/track", Middleware.OptionalAuth(), TrackInteraction)
	}

	interactionGroup := r.Group("/interaction", Middleware.AuthRequired())
	{
		interactionGroup.POST("/like", ToggleLike)
		interactionGroup.POST("/save", ToggleSave)
		interactionGroup.POST("/follow", ToggleFollow)
	}

	// Delete routes
	r.DELETE("/interaction/video/:id", Middleware.AuthRequired(), DeleteVideo)
	r.DELETE("/video/:id", Middleware.AuthRequired(), DeleteVideo)

	// View Count Endpoint
	r.POST("/video/:id/view", IncrementViews)

	// Single Video Endpoint
	r.GET("/video/:id", Middleware.OptionalAuth(), GetVideoById)

	// Reporting
	r.POST("/report", Middleware.AuthRequired(), SubmitReport)
}
