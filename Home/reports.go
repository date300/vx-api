package Home

import (
	"net/http"
	"vx-api/Config"
	"github.com/gin-gonic/gin"
)

// SubmitReport handles POST /api/v1/report
func SubmitReport(c *gin.Context) {
	myID, _ := c.Get("userID")
	myIDUint := myID.(uint)

	var input struct {
		TargetID   uint   `json:"target_id" binding:"required"`
		TargetType string `json:"target_type" binding:"required"` // "USER", "VIDEO", "COMMENT"
		Reason     string `json:"reason" binding:"required"`
		Details    string `json:"details"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": false, "message": "Invalid input"})
		return
	}

	report := Report{
		ReporterID: myIDUint,
		TargetID:   input.TargetID,
		TargetType: input.TargetType,
		Reason:     input.Reason,
		Details:    input.Details,
	}

	if err := Config.DB.Create(&report).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": false, "message": "Failed to submit report"})
		return
	}

	// Track as negative interaction if it's a video
	if input.TargetType == "VIDEO" {
		interaction := Interaction{
			UserID:  myIDUint,
			VideoID: input.TargetID,
			Type:    "REPORT",
		}
		Config.DB.Create(&interaction)
	}

	c.JSON(http.StatusOK, gin.H{
		"status":  true,
		"message": "Report submitted successfully. Thank you for helping keep our community safe.",
	})
}
