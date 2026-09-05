package Middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"vx-api/Utils"

	"github.com/gin-gonic/gin"
)

func TestAuthRequiredSetsUserIDFromBearerToken(t *testing.T) {
	gin.SetMode(gin.TestMode)

	token, _, err := Utils.GenerateTokens(42)
	if err != nil {
		t.Fatalf("failed to generate token: %v", err)
	}

	router := gin.New()
	router.Use(AuthRequired())
	router.GET("/protected", func(c *gin.Context) {
		userID, exists := c.Get("userID")
		if !exists {
			t.Fatalf("userID was not set in context")
		}

		if got, ok := userID.(uint); !ok || got != 42 {
			t.Fatalf("expected userID 42, got %v", userID)
		}

		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}
}

func TestAuthRequiredRejectsMissingToken(t *testing.T) {
	gin.SetMode(gin.TestMode)

	router := gin.New()
	router.Use(AuthRequired())
	router.GET("/protected", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)

	router.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected status 401, got %d", w.Code)
	}
}
