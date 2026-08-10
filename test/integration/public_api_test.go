package integration

import (
	"net/http"
	"strings"
	"testing"

	api "github.com/kannachi323/misty/server/internal/platform/httpapi"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestPublicAccountResponsesHideLicenseID(t *testing.T) {
	database := openIntegrationDatabase(t)

	registerRec := performJSONRequest(t, api.Register(database), http.MethodPost, "/register", map[string]string{
		"name":     "Ada Lovelace",
		"username": "ada_lovelace",
		"email":    "ada@example.com",
		"password": "correct horse battery staple",
	})
	if registerRec.Code != http.StatusCreated {
		t.Fatalf("register status = %d, want %d, body = %q", registerRec.Code, http.StatusCreated, registerRec.Body.String())
	}

	registerBody := decodeJSONResponse(t, registerRec)
	if _, ok := registerBody["license_id"]; ok {
		t.Fatalf("register response unexpectedly exposed license_id: %#v", registerBody)
	}
	if registerBody["username"] != "ada_lovelace" {
		t.Fatalf("register username = %#v, want ada_lovelace", registerBody["username"])
	}

	userID, ok := registerBody["user_id"].(string)
	if !ok || userID == "" {
		t.Fatalf("register response missing user_id: %#v", registerBody)
	}

	loginRec := performJSONRequest(t, api.Login(database), http.MethodPost, "/login", map[string]string{
		"email":    "ada@example.com",
		"password": "correct horse battery staple",
	})
	if loginRec.Code != http.StatusOK {
		t.Fatalf("login status = %d, want %d, body = %q", loginRec.Code, http.StatusOK, loginRec.Body.String())
	}

	loginBody := decodeJSONResponse(t, loginRec)
	if _, ok := loginBody["license_id"]; ok {
		t.Fatalf("login response unexpectedly exposed license_id: %#v", loginBody)
	}

	sessionCookie := requireCookie(t, loginRec, sessionCookieName)

	meRec := performJSONRequest(t, api.GetMe(database), http.MethodGet, "/me", nil, sessionCookie)
	if meRec.Code != http.StatusOK {
		t.Fatalf("/me status = %d, want %d, body = %q", meRec.Code, http.StatusOK, meRec.Body.String())
	}

	meBody := decodeJSONResponse(t, meRec)
	if _, ok := meBody["license_id"]; ok {
		t.Fatalf("/me response unexpectedly exposed license_id: %#v", meBody)
	}
	if meBody["username"] != "ada_lovelace" {
		t.Fatalf("/me username = %#v, want ada_lovelace", meBody["username"])
	}
	if got, ok := meBody["allows_use"].(bool); !ok || !got {
		t.Fatalf("/me allows_use = %#v, want true", meBody["allows_use"])
	}
	if got, ok := meBody["tier"].(string); !ok || got != "basic" {
		t.Fatalf("/me tier = %#v, want basic", meBody["tier"])
	}
}

func TestBillingUsageExposesOnlyCustomerSafeAgentUsage(t *testing.T) {
	database := openIntegrationDatabase(t)
	registerRec := performJSONRequest(t, api.Register(database), http.MethodPost, "/register", map[string]string{
		"name": "Usage User", "username": "usage_user", "email": "usage@example.com", "password": "password123",
	})
	if registerRec.Code != http.StatusCreated {
		t.Fatalf("register status = %d: %s", registerRec.Code, registerRec.Body.String())
	}
	sessionCookie := requireCookie(t, registerRec, sessionCookieName)
	recorder := performJSONRequest(t, api.GetBillingUsage(database), http.MethodGet, "/billing/usage", nil, sessionCookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("billing usage status = %d: %s", recorder.Code, recorder.Body.String())
	}
	body := decodeJSONResponse(t, recorder)
	usage, ok := body["agent_usage"].(map[string]any)
	if !ok {
		t.Fatalf("agent_usage = %#v", body["agent_usage"])
	}
	for _, key := range []string{"percentage_used", "available", "paused", "reset_at", "plan"} {
		if _, exists := usage[key]; !exists {
			t.Fatalf("agent_usage missing %q: %#v", key, usage)
		}
	}
	raw := strings.ToLower(recorder.Body.String())
	for _, forbidden := range []string{"microusd", "weekly_allowance", "weekly_remaining", "provider_cost", "charged_microusd", "credits"} {
		if strings.Contains(raw, forbidden) {
			t.Fatalf("billing usage exposed internal field %q: %s", forbidden, recorder.Body.String())
		}
	}
}

func TestGetMeExposesBillingKindAndLifetimeFallback(t *testing.T) {
	database := openIntegrationDatabase(t)

	registerRec := performJSONRequest(t, api.Register(database), http.MethodPost, "/register", map[string]string{
		"name":     "Upgrade User",
		"username": "upgrade_user",
		"email":    "upgrade@example.com",
		"password": "correct horse battery staple",
	})
	if registerRec.Code != http.StatusCreated {
		t.Fatalf("register status = %d, want %d, body = %q", registerRec.Code, http.StatusCreated, registerRec.Body.String())
	}

	loginRec := performJSONRequest(t, api.Login(database), http.MethodPost, "/login", map[string]string{
		"email":    "upgrade@example.com",
		"password": "correct horse battery staple",
	})
	if loginRec.Code != http.StatusOK {
		t.Fatalf("login status = %d, want %d, body = %q", loginRec.Code, http.StatusOK, loginRec.Body.String())
	}
	sessionCookie := requireCookie(t, loginRec, sessionCookieName)

	meRec := performJSONRequest(t, api.GetMe(database), http.MethodGet, "/me", nil, sessionCookie)
	if meRec.Code != http.StatusOK {
		t.Fatalf("/me status = %d, want %d, body = %q", meRec.Code, http.StatusOK, meRec.Body.String())
	}
	meBody := decodeJSONResponse(t, meRec)
	billingBody, _ := meBody["billing"].(map[string]any)
	if billingBody["kind"] != "free" {
		t.Fatalf("expected new basic user to have free billing kind: %#v", meBody)
	}

	loginBody := decodeJSONResponse(t, loginRec)
	userID, ok := loginBody["user_id"].(string)
	if !ok || userID == "" {
		t.Fatalf("login response missing user_id: %#v", loginBody)
	}

	user, err := database.GetUserByID(userID)
	if err != nil || user == nil {
		t.Fatalf("GetUserByID() error = %v, user = %#v", err, user)
	}
	if err := database.SetLicenseStateByID(user.LicenseID, db.TierPro, db.LicenseStatusActive, nil); err != nil {
		t.Fatalf("SetLicenseStateByID() error = %v", err)
	}
	legacyTier := db.TierPro
	if err := database.SetLegacyTierByID(user.LicenseID, &legacyTier); err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertStripePurchase(&db.StripePurchase{
		UserID:                  user.ID,
		LicenseID:               user.LicenseID,
		TierPurchased:           db.TierPersonal,
		StripeCheckoutSessionID: "cs_personal_upgrade_test",
		StripePaymentIntentID:   "pi_personal_upgrade_test",
		StripeChargeID:          "ch_personal_upgrade_test",
		Status:                  "completed",
		EventSource:             "test",
	}); err != nil {
		t.Fatalf("UpsertStripePurchase() error = %v", err)
	}

	upgradedMeRec := performJSONRequest(t, api.GetMe(database), http.MethodGet, "/me", nil, sessionCookie)
	if upgradedMeRec.Code != http.StatusOK {
		t.Fatalf("/me upgraded status = %d, want %d, body = %q", upgradedMeRec.Code, http.StatusOK, upgradedMeRec.Body.String())
	}
	upgradedMeBody := decodeJSONResponse(t, upgradedMeRec)
	upgradedBilling, _ := upgradedMeBody["billing"].(map[string]any)
	if upgradedBilling["kind"] != "lifetime" || upgradedMeBody["tier"] != "pro" {
		t.Fatalf("expected grandfathered user to expose lifetime Pro billing: %#v", upgradedMeBody)
	}
}

func TestSettingsEndpointsExposeAndPersistEmailUpdatesPreference(t *testing.T) {
	database := openIntegrationDatabase(t)

	registerRec := performJSONRequest(t, api.Register(database), http.MethodPost, "/register", map[string]string{
		"name":     "Settings User",
		"username": "settings_user",
		"email":    "settings@example.com",
		"password": "correct horse battery staple",
	})
	if registerRec.Code != http.StatusCreated {
		t.Fatalf("register status = %d, want %d, body = %q", registerRec.Code, http.StatusCreated, registerRec.Body.String())
	}

	loginRec := performJSONRequest(t, api.Login(database), http.MethodPost, "/login", map[string]string{
		"email":    "settings@example.com",
		"password": "correct horse battery staple",
	})
	if loginRec.Code != http.StatusOK {
		t.Fatalf("login status = %d, want %d, body = %q", loginRec.Code, http.StatusOK, loginRec.Body.String())
	}
	sessionCookie := requireCookie(t, loginRec, sessionCookieName)

	getRec := performJSONRequest(t, api.GetSettings(database), http.MethodGet, "/me/settings", nil, sessionCookie)
	if getRec.Code != http.StatusOK {
		t.Fatalf("/me/settings status = %d, want %d, body = %q", getRec.Code, http.StatusOK, getRec.Body.String())
	}
	getBody := decodeJSONResponse(t, getRec)
	if enabled, _ := getBody["email_updates_enabled"].(bool); enabled {
		t.Fatalf("expected email updates to default false: %#v", getBody)
	}

	updateRec := performJSONRequest(t, api.UpdateSettings(database), http.MethodPut, "/me/settings", map[string]bool{
		"email_updates_enabled": true,
	}, sessionCookie)
	if updateRec.Code != http.StatusOK {
		t.Fatalf("PUT /me/settings status = %d, want %d, body = %q", updateRec.Code, http.StatusOK, updateRec.Body.String())
	}

	getUpdatedRec := performJSONRequest(t, api.GetSettings(database), http.MethodGet, "/me/settings", nil, sessionCookie)
	if getUpdatedRec.Code != http.StatusOK {
		t.Fatalf("/me/settings after update status = %d, want %d, body = %q", getUpdatedRec.Code, http.StatusOK, getUpdatedRec.Body.String())
	}
	getUpdatedBody := decodeJSONResponse(t, getUpdatedRec)
	if enabled, _ := getUpdatedBody["email_updates_enabled"].(bool); !enabled {
		t.Fatalf("expected email updates to persist true: %#v", getUpdatedBody)
	}
}
