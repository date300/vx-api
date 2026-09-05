package Auth

import "testing"

func TestIsOTPValidAllowsAnyCodeInDevelopmentMode(t *testing.T) {
	if !isOTPValid("123456", "654321", true) {
		t.Fatal("expected any six-digit code to be accepted in development mode")
	}
}

func TestIsOTPValidRejectsWrongCodeInProductionMode(t *testing.T) {
	if isOTPValid("123456", "654321", false) {
		t.Fatal("expected mismatch to be rejected in production mode")
	}
}

func TestIsOTPValidMatchesStoredCode(t *testing.T) {
	if !isOTPValid("123456", "123456", false) {
		t.Fatal("expected matching code to be accepted")
	}
}
