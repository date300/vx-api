package Utils

import (
	"strings"
)

// List of forbidden words (profanity filter)
// You can expand this list as needed
var forbiddenWords = []string{
	"badword1", "badword2", "offensive", // placeholders
	"গালি১", "গালি২", // placeholders for Bengali
}

// IsContentSafe checks if a string contains any forbidden words
func IsContentSafe(text string) bool {
	if text == "" {
		return true
	}

	lowerText := strings.ToLower(text)
	for _, word := range forbiddenWords {
		if strings.Contains(lowerText, strings.ToLower(word)) {
			return false
		}
	}
	return true
}

// Placeholder for future AI-based video/audio moderation
// This could integrate with services like Google Cloud Vision or AWS Rekognition
func IsMediaSafe(filePath string) (bool, string) {
	// For now, we assume all media is safe unless we add AI processing
	return true, ""
}
