package Auth

import (
	"errors"
	"fmt"
	"net/http"
	"net/smtp"
	"time"

	"vx-api/Config"
	
	"vx-api/Utils"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"gorm.io/gorm"
)

// RegisterRoutes handles all Auth related endpoints
func RegisterRoutes(r *gin.RouterGroup) {
	authGroup := r.Group("/auth")
	{
		authGroup.POST("/email-request", RequestEmailOTP)
		authGroup.POST("/email-verify", VerifyEmailOTP)
		authGroup.POST("/social", SocialAuth)
		authGroup.POST("/refresh", RefreshToken)
	}
}

func isOTPValid(receivedOTP, storedOTP string, isDevelopment bool) bool {
	if isDevelopment && len(receivedOTP) == 6 {
		return true
	}
	return receivedOTP == storedOTP
}

func sendSecureEmail(targetEmail, otpCode string) error {
	if Config.SMTPDisabled {
		fmt.Printf("SMTP disabled; OTP for %s is %s\n", targetEmail, otpCode)
		return nil
	}

	from := Config.SMTPFrom
	if from == "" {
		from = "support@easysarvice.com"
	}
	password := Config.SMTPPassword
	smtpHost := Config.SMTPHost
	if smtpHost == "" {
		smtpHost = "easysarvice.com"
	}
	smtpPort := Config.SMTPPort
	if smtpPort == "" {
		smtpPort = "587"
	}

	subject := "Subject: VX App Login Verification Code\n"
	mime := "MIME-version: 1.0;\nContent-Type: text/html; charset=\"UTF-8\";\n\n"
	body := fmt.Sprintf(`
                <div style="font-family: Arial, sans-serif; max-width: 400px; margin: auto; padding: 20px; border: 1px solid #eee; border-radius: 10px; background-color: #ffffff;">
                        <h2 style="color: #333; text-align: center;">VX Verification</h2>
                        <p style="color: #555;">Your secure login one-time password (OTP) is:</p>
                        <div style="background: #f4f4f4; padding: 15px; text-align: center; font-size: 26px; font-weight: bold; letter-spacing: 5px; color: #ff0050; border-radius: 6px;">
                                %s
                        </div>
                        <p style="font-size: 12px; color: #777; margin-top: 20px; text-align: center;">This code is valid for 5 minutes. Do not share it with anyone.</p>
                </div>
        `, otpCode)

	msg := []byte(subject + mime + body)
	auth := smtp.PlainAuth("", from, password, smtpHost)
	return smtp.SendMail(smtpHost+":"+smtpPort, auth, from, []string{targetEmail}, msg)
}

// RequestEmailOTP handles POST /api/v1/auth/email-request
func RequestEmailOTP(c *gin.Context) {
	var input struct {
		Email string `json:"email" binding:"required,email"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": false, "error": "Invalid email format"})
		return
	}

	otpCode := Utils.GenerateOTP()

	if err := sendSecureEmail(input.Email, otpCode); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": false, "error": "Email transmission failed"})
		return
	}

	expiresAt := time.Now().Add(5 * time.Minute)
	var user User

	result := Config.DB.Where("email = ? AND provider = ?", input.Email, "email").First(&user)

	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		username := fmt.Sprintf("user_%d", time.Now().UnixNano())
		user = User{
			Email:        input.Email,
			Provider:     "email",
			OTPCode:      otpCode,
			OTPExpiresAt: &expiresAt,
			Username:     &username,
		}
		Config.DB.Create(&user)
	} else {
		user.OTPCode = otpCode
		user.OTPExpiresAt = &expiresAt
		Config.DB.Save(&user)
	}

	response := gin.H{"status": true, "message": "Secure OTP sent successfully"}
	if Config.SMTPDisabled {
		response["otp_code"] = otpCode
	}

	c.JSON(http.StatusOK, response)
}

// VerifyEmailOTP handles POST /api/v1/auth/email-verify
func VerifyEmailOTP(c *gin.Context) {
	var input struct {
		Email string `json:"email" binding:"required,email"`
		OTP   string `json:"otp" binding:"required,len=6"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": false, "message": "Invalid payload data"})
		return
	}

	isDevelopment := Config.AppEnv != "production"
	var user User
	result := Config.DB.Where("email = ? AND provider = ?", input.Email, "email").First(&user)

	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		if isDevelopment {
			username := fmt.Sprintf("user_%d", time.Now().UnixNano())
			user = User{
				Email:       input.Email,
				Provider:    "email",
				IsOnboarded: false,
				Username:    &username,
			}
			if err := Config.DB.Create(&user).Error; err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"status": false, "message": "Failed to create user in development: " + err.Error()})
				return
			}
		} else {
			c.JSON(http.StatusUnauthorized, gin.H{"status": false, "message": "User session not found"})
			return
		}
	} else if result.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": false, "message": "Database error: " + result.Error.Error()})
		return
	}

	if isDevelopment {
		if len(input.OTP) != 6 {
			c.JSON(http.StatusBadRequest, gin.H{"status": false, "message": "OTP must be 6 digits"})
			return
		}
	} else {
		if user.OTPCode == "" || !isOTPValid(input.OTP, user.OTPCode, false) {
			c.JSON(http.StatusUnauthorized, gin.H{"status": false, "message": "Invalid validation code"})
			return
		}

		if user.OTPExpiresAt != nil && time.Now().After(*user.OTPExpiresAt) {
			c.JSON(http.StatusUnauthorized, gin.H{"status": false, "message": "OTP token has expired"})
			return
		}
	}

	user.OTPCode = ""
	user.OTPExpiresAt = nil

	accessToken, refreshToken, err := Utils.GenerateTokens(user.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": false, "error": "Token encryption error"})
		return
	}

	user.RefreshToken = refreshToken
	Config.DB.Save(&user)

	isNewUser := !user.IsOnboarded

	c.JSON(http.StatusOK, gin.H{
		"status":        true,
		"access_token":  accessToken,
		"refresh_token": refreshToken,
		"is_new_user":   isNewUser,
		"user":          gin.H{"id": user.ID, "email": user.Email, "username": user.Username},
	})
}

// RefreshToken handles POST /api/v1/auth/refresh
func RefreshToken(c *gin.Context) {
	var input struct {
		RefreshToken string `json:"refresh_token" binding:"required"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": false, "error": "Invalid request payload"})
		return
	}

	// Parse and validate refresh token
	token, err := jwt.Parse(input.RefreshToken, func(token *jwt.Token) (interface{}, error) {
		return []byte(Config.JWTSecret), nil
	})

	if err != nil || !token.Valid {
		c.JSON(http.StatusUnauthorized, gin.H{"status": false, "error": "Invalid or expired refresh token"})
		return
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"status": false, "error": "Invalid token claims"})
		return
	}

	var userID uint
	switch value := claims["user_id"].(type) {
	case float64:
		userID = uint(value)
	case int:
		userID = uint(value)
	default:
		c.JSON(http.StatusUnauthorized, gin.H{"status": false, "error": "User identity not found in token"})
		return
	}

	// Verify refresh token against database
	var user User
	if err := Config.DB.Where("id = ? AND refresh_token = ?", userID, input.RefreshToken).First(&user).Error; err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"status": false, "error": "Refresh token not found or mismatch"})
		return
	}

	// Generate new access token (and optionally a new refresh token)
	newAccessToken, newRefreshToken, err := Utils.GenerateTokens(userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": false, "error": "Token generation error"})
		return
	}

	// Update refresh token in database
	user.RefreshToken = newRefreshToken
	Config.DB.Save(&user)

	c.JSON(http.StatusOK, gin.H{
		"status":        true,
		"access_token":  newAccessToken,
		"refresh_token": newRefreshToken,
	})
}

// SocialAuth handles POST /api/v1/auth/social
func SocialAuth(c *gin.Context) {
	var input struct {
		Provider    string `json:"provider" binding:"required"`
		SocialToken string `json:"social_token" binding:"required"`
		Email       string `json:"email" binding:"required,email"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": false, "error": "Invalid request payload"})
		return
	}

	if input.Provider != "google" {
		c.JSON(http.StatusBadRequest, gin.H{"status": false, "error": "Unsupported provider type"})
		return
	}

	var user User
	result := Config.DB.Where("email = ? AND provider = ?", input.Email, "google").First(&user)

	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		user = User{
			Email:    input.Email,
			Provider: "google",
		}
		Config.DB.Create(&user)
	}

	accessToken, refreshToken, err := Utils.GenerateTokens(user.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": false, "error": "Token generation error"})
		return
	}

	user.RefreshToken = refreshToken
	Config.DB.Save(&user)

	isNewUser := !user.IsOnboarded

	c.JSON(http.StatusOK, gin.H{
		"status":        true,
		"access_token":  accessToken,
		"refresh_token": refreshToken,
		"is_new_user":   isNewUser,
		"user":          gin.H{"id": user.ID, "email": user.Email, "username": user.Username},
	})
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
