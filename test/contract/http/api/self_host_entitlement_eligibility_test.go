package api

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/kannachi323/misty/server/internal/platform/entitlement"
	. "github.com/kannachi323/misty/server/internal/platform/httpapi"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"github.com/kannachi323/misty/server/internal/platform/security"
	"github.com/kannachi323/misty/server/test/testkit"
)

func TestSelfHostEntitlementEligibilityAndTrialExpiry(t *testing.T) {
	database := testkit.OpenDatabase(t)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pkcs8, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("MISTY_DEPLOYMENT_MODE", "hosted")
	t.Setenv("MISTY_SELF_HOST_ENTITLEMENT_PRIVATE_KEY", base64.StdEncoding.EncodeToString(pkcs8))
	t.Setenv("MISTY_SELF_HOST_ENTITLEMENT_KEY_ID", "test-key")
	t.Setenv("MISTY_SELF_HOST_ENTITLEMENT_SUBJECT_SECRET", base64.StdEncoding.EncodeToString(make([]byte, 32)))

	free := createEntitlementTestUser(t, database, "free")
	if recorder := requestEntitlement(t, database, free.ID, "free-token"); recorder.Code != http.StatusForbidden {
		t.Fatalf("free status = %d", recorder.Code)
	}

	trial := createEntitlementTestUser(t, database, "trial")
	if started, err := database.StartTrialByUserID(trial.ID, 2*time.Hour); err != nil || !started {
		t.Fatalf("start trial = %v, %v", started, err)
	}
	trialResponse := requestEntitlement(t, database, trial.ID, "trial-token")
	if trialResponse.Code != http.StatusOK {
		t.Fatalf("trial status = %d: %s", trialResponse.Code, trialResponse.Body.String())
	}
	trialClaims := entitlementClaimsFromResponse(t, trialResponse, publicKey)
	if time.Until(time.Unix(trialClaims.ExpiresAt, 0)) > 2*time.Hour {
		t.Fatalf("trial proof expires after trial: %s", time.Unix(trialClaims.ExpiresAt, 0))
	}

	canceled := createEntitlementTestUser(t, database, "canceled")
	canceledEnd := time.Now().UTC().Add(30 * 24 * time.Hour)
	if err := database.UpsertStripeSubscription(&db.StripeSubscription{
		UserID: canceled.ID, LicenseID: canceled.LicenseID, StripeSubscriptionID: "sub_canceled",
		StripeCustomerID: "cus_canceled", StripePriceID: "price_pro", Tier: db.TierPro,
		BillingInterval: "month", Status: "canceled", CurrentPeriodEnd: &canceledEnd,
	}); err != nil {
		t.Fatal(err)
	}
	if recorder := requestEntitlement(t, database, canceled.ID, "canceled-token"); recorder.Code != http.StatusForbidden {
		t.Fatalf("canceled status = %d", recorder.Code)
	}

	paid := createEntitlementTestUser(t, database, "paid")
	paidEnd := time.Now().UTC().Add(30 * 24 * time.Hour)
	if err := database.UpsertStripeSubscription(&db.StripeSubscription{
		UserID: paid.ID, LicenseID: paid.LicenseID, StripeSubscriptionID: "sub_paid",
		StripeCustomerID: "cus_paid", StripePriceID: "price_pro", Tier: db.TierPro,
		BillingInterval: "month", Status: db.SubscriptionStatusActive, CurrentPeriodEnd: &paidEnd,
	}); err != nil {
		t.Fatal(err)
	}
	paidResponse := requestEntitlement(t, database, paid.ID, "paid-token")
	if paidResponse.Code != http.StatusOK {
		t.Fatalf("paid status = %d: %s", paidResponse.Code, paidResponse.Body.String())
	}
	paidClaims := entitlementClaimsFromResponse(t, paidResponse, publicKey)
	proofLifetime := time.Unix(paidClaims.ExpiresAt, 0).Sub(time.Unix(paidClaims.IssuedAt, 0))
	if proofLifetime > entitlement.MaxLifetime {
		t.Fatalf("paid proof lifetime = %s", proofLifetime)
	}
}

func createEntitlementTestUser(t *testing.T, database *db.Database, suffix string) *db.User {
	t.Helper()
	user, err := database.CreateUserWithUsername(
		"Entitlement "+suffix, "entitlement_"+suffix, "entitlement-"+suffix+"@example.com", "password123",
	)
	if err != nil {
		t.Fatal(err)
	}
	return user
}

func requestEntitlement(t *testing.T, database *db.Database, userID, token string) *httptest.ResponseRecorder {
	t.Helper()
	if err := database.CreateSession(security.HashToken(token), userID); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/billing/self-host-entitlement", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	recorder := httptest.NewRecorder()
	MintSelfHostedEntitlement(database).ServeHTTP(recorder, request)
	return recorder
}

func entitlementClaimsFromResponse(t *testing.T, recorder *httptest.ResponseRecorder, publicKey ed25519.PublicKey) entitlement.Claims {
	t.Helper()
	var payload struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	claims, err := entitlement.Verify(payload.Token, map[string]ed25519.PublicKey{"test-key": publicKey}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	return claims
}
