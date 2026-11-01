package integration

import (
	"net/http"
	"testing"
	"time"

	"github.com/kannachi323/misty/server/internal/platform/db"
	api "github.com/kannachi323/misty/server/internal/platform/httpapi"
	"github.com/kannachi323/misty/server/internal/platform/security"
)

// The server sends no email: a setup key (a sign-in link from the operator
// API or the console) resets a forgotten password.
func TestSetupKeyResetsAPassword(t *testing.T) {
	database := openIntegrationDatabase(t)

	user, err := database.CreateUser("reset@example.com", "old-password")
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	key, err := api.GenerateSetupKey()
	if err != nil {
		t.Fatal(err)
	}
	if err := database.IssueSetupKey(t.Context(), user.ID, security.HashToken(key), time.Now().Add(db.SetupKeyTTL)); err != nil {
		t.Fatal(err)
	}

	redeemRec := performJSONRequest(t, api.RedeemSetupKey(database), http.MethodPost, "/setup-keys/redeem", map[string]string{"setup_key": key})
	if redeemRec.Code != http.StatusOK {
		t.Fatalf("redeem status = %d, body = %q", redeemRec.Code, redeemRec.Body.String())
	}
	session := requireCookie(t, redeemRec, sessionCookieName)
	reset, _ := decodeJSONResponse(t, redeemRec)["password_reset_token"].(string)
	if reset == "" {
		t.Fatal("a password account's redeem carries no password_reset_token")
	}

	// Without the reset token the old password is still needed.
	refused := performJSONRequest(t, api.ChangePassword(database), http.MethodPut, "/me/password", map[string]any{
		"current_password": "", "new_password": "new-password",
	}, session)
	if refused.Code != http.StatusUnauthorized {
		t.Fatalf("change without the old password = %d, want 401", refused.Code)
	}
	changed := performJSONRequest(t, api.ChangePassword(database), http.MethodPut, "/me/password", map[string]any{
		"reset_token": reset, "new_password": "new-password",
	}, session)
	if changed.Code != http.StatusOK {
		t.Fatalf("reset status = %d, body = %q", changed.Code, changed.Body.String())
	}
	again := performJSONRequest(t, api.ChangePassword(database), http.MethodPut, "/me/password", map[string]any{
		"reset_token": reset, "new_password": "newer-password",
	}, session)
	if again.Code != http.StatusUnauthorized {
		t.Fatalf("a used reset token = %d, want 401", again.Code)
	}

	loginRec := performJSONRequest(t, api.Login(database), http.MethodPost, "/login", map[string]string{
		"email":    "reset@example.com",
		"password": "new-password",
	})
	if loginRec.Code != http.StatusOK {
		t.Fatalf("Login after reset status = %d, want %d, body = %q", loginRec.Code, http.StatusOK, loginRec.Body.String())
	}
}
