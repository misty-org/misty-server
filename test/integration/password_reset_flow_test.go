package integration

import (
	"net/http"
	"net/url"
	"testing"

	api "github.com/kannachi323/misty/server/internal/platform/httpapi"
	"github.com/kannachi323/misty/server/internal/platform/security"
)

func TestPasswordResetEndToEnd(t *testing.T) {
	database := openIntegrationDatabase(t)
	sender := &fakePasswordResetSender{}

	service, err := api.NewPasswordResetService(
		database,
		sender,
		"https://api.example.com/auth/reset/start",
		"https://app.example.com/reset",
	)
	if err != nil {
		t.Fatalf("NewPasswordResetService() error = %v", err)
	}

	user, err := database.CreateUser("Reset User", "reset@example.com", "old-password")
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}

	forgotRec := performJSONRequest(t, service.Forgot(), http.MethodPost, "/auth/forgot", map[string]string{
		"email": "reset@example.com",
	})
	if forgotRec.Code != http.StatusAccepted {
		t.Fatalf("Forgot status = %d, want %d, body = %q", forgotRec.Code, http.StatusAccepted, forgotRec.Body.String())
	}
	if len(sender.calls) != 1 {
		t.Fatalf("password reset emails sent = %d, want 1", len(sender.calls))
	}

	resetLink, err := url.Parse(sender.calls[0].resetLink)
	if err != nil {
		t.Fatalf("Parse(resetLink) error = %v", err)
	}
	token := resetLink.Query().Get("token")
	if token == "" {
		t.Fatal("reset token missing from reset link")
	}
	if getStoredPasswordResetTokenHash(t, database, user.ID) != security.HashToken(token) {
		t.Fatal("stored password reset token hash did not match emailed token")
	}

	startReq := performJSONRequest(t, service.Start(), http.MethodGet, "/auth/reset/start?token="+token, nil)
	if startReq.Code != http.StatusSeeOther {
		t.Fatalf("Start status = %d, want %d", startReq.Code, http.StatusSeeOther)
	}
	resetCookie := requireCookie(t, startReq, "misty_reset_token")

	validateRec := performJSONRequest(t, service.Validate(), http.MethodGet, "/auth/reset/validate", nil, resetCookie)
	if validateRec.Code != http.StatusOK {
		t.Fatalf("Validate status = %d, want %d, body = %q", validateRec.Code, http.StatusOK, validateRec.Body.String())
	}

	resetRec := performJSONRequest(t, service.Reset(), http.MethodPost, "/auth/reset", map[string]string{
		"new_password": "new-password",
	}, resetCookie)
	if resetRec.Code != http.StatusOK {
		t.Fatalf("Reset status = %d, want %d, body = %q", resetRec.Code, http.StatusOK, resetRec.Body.String())
	}

	loginRec := performJSONRequest(t, api.Login(database), http.MethodPost, "/login", map[string]string{
		"email":    "reset@example.com",
		"password": "new-password",
	})
	if loginRec.Code != http.StatusOK {
		t.Fatalf("Login after reset status = %d, want %d, body = %q", loginRec.Code, http.StatusOK, loginRec.Body.String())
	}
}

func TestPasswordResetInvalidTokenPaths(t *testing.T) {
	database := openIntegrationDatabase(t)
	sender := &fakePasswordResetSender{}

	service, err := api.NewPasswordResetService(
		database,
		sender,
		"https://api.example.com/auth/reset/start",
		"https://app.example.com/reset",
	)
	if err != nil {
		t.Fatalf("NewPasswordResetService() error = %v", err)
	}

	validateRec := performJSONRequest(t, service.Validate(), http.MethodGet, "/auth/reset/validate", nil)
	if validateRec.Code != http.StatusNotFound {
		t.Fatalf("Validate missing cookie status = %d, want %d", validateRec.Code, http.StatusNotFound)
	}

	startRec := performJSONRequest(t, service.Start(), http.MethodGet, "/auth/reset/start?token=missing", nil)
	if startRec.Code != http.StatusSeeOther {
		t.Fatalf("Start invalid token status = %d, want %d", startRec.Code, http.StatusSeeOther)
	}

	resetRec := performJSONRequest(t, service.Reset(), http.MethodPost, "/auth/reset", map[string]string{
		"new_password": "new-password",
	}, &http.Cookie{Name: "misty_reset_token", Value: "missing"})
	if resetRec.Code != http.StatusBadRequest {
		t.Fatalf("Reset invalid token status = %d, want %d", resetRec.Code, http.StatusBadRequest)
	}
}

func TestForgotPasswordUnknownUserStillAccepted(t *testing.T) {
	database := openIntegrationDatabase(t)
	sender := &fakePasswordResetSender{}

	service, err := api.NewPasswordResetService(
		database,
		sender,
		"https://api.example.com/auth/reset/start",
		"https://app.example.com/reset",
	)
	if err != nil {
		t.Fatalf("NewPasswordResetService() error = %v", err)
	}

	rec := performJSONRequest(t, service.Forgot(), http.MethodPost, "/auth/forgot", map[string]string{
		"email": "missing@example.com",
	})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("Forgot status = %d, want %d", rec.Code, http.StatusAccepted)
	}
	if len(sender.calls) != 0 {
		t.Fatalf("password reset emails sent = %d, want 0", len(sender.calls))
	}
}

func TestPasswordResetConstructorValidation(t *testing.T) {
	database := openIntegrationDatabase(t)
	sender := &fakePasswordResetSender{}

	if _, err := api.NewPasswordResetService(nil, sender, "https://api.example.com/reset", "https://app.example.com/reset"); err == nil {
		t.Fatal("NewPasswordResetService() succeeded with nil database")
	}
	if _, err := api.NewPasswordResetService(database, nil, "https://api.example.com/reset", "https://app.example.com/reset"); err == nil {
		t.Fatal("NewPasswordResetService() succeeded with nil sender")
	}
	if _, err := api.NewPasswordResetService(database, sender, "http://example.com/reset", "https://app.example.com/reset"); err == nil {
		t.Fatal("NewPasswordResetService() succeeded with non-localhost http start URL")
	}
}
