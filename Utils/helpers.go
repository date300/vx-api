package Utils

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"time"

	"vx-api/Config"
	"os"
	"path/filepath"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

// FileExists checks if a media file exists on disk
func FileExists(url string) bool {
	if url == "" {
		return false
	}
	// Strip base URL if present
	cleanURL := url
	baseURL := strings.TrimSuffix(Config.BaseURL, "/")
	if strings.HasPrefix(url, baseURL) {
		cleanURL = strings.TrimPrefix(url, baseURL)
	}

	// Ensure it starts with / for joining
	if !strings.HasPrefix(cleanURL, "/") {
		cleanURL = "/" + cleanURL
	}

	path := filepath.Join("public", cleanURL)
	_, err := os.Stat(path)
	return err == nil
}

// সিকিউর ৬ ডিজিটের ওটিপি জেনারেটর
func GenerateOTP() string {
	max := big.NewInt(900000)
	n, _ := rand.Int(rand.Reader, max)
	return fmt.Sprintf("%06d", n.Add(n, big.NewInt(100000)))
}

// প্রোডাকশন-গ্রেড JWT ডুয়াল টোকেন ইঞ্জিন
func GenerateTokens(userID uint) (string, string, error) {
	// ২০ বছরের জন্য অ্যাক্সেস টোকেন (কার্যত পার্মানেন্ট লগইন)
	accessTokenClaims := jwt.MapClaims{
		"user_id": userID,
		"exp":     time.Now().Add(20 * 365 * 24 * time.Hour).Unix(),
	}
	accessToken := jwt.NewWithClaims(jwt.SigningMethodHS256, accessTokenClaims)
	at, err := accessToken.SignedString([]byte(Config.JWTSecret))
	if err != nil {
		return "", "", err
	}

	// ২০ বছরের জন্য রিফ্রেশ টোকেন
	refreshTokenClaims := jwt.MapClaims{
		"user_id": userID,
		"exp":     time.Now().Add(20 * 365 * 24 * time.Hour).Unix(),
	}
	refreshToken := jwt.NewWithClaims(jwt.SigningMethodHS256, refreshTokenClaims)
	rt, err := refreshToken.SignedString([]byte(Config.JWTSecret))

	return at, rt, err
}
