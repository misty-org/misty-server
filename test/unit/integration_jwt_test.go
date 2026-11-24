package unit

import (
	"encoding/base64"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/misty-org/misty-server/internal/accounts"
	"github.com/misty-org/misty-server/internal/platform/security"
)

func integrationSigner(t *testing.T) *security.SessionSigner {
	t.Helper()
	t.Setenv("MISTY_AUTH_SIGNING_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("b", 32))))
	t.Setenv("MISTY_AUTH_SIGNING_KEY_PREVIOUS", "")
	signer, err := security.SessionSignerFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	return signer
}

func TestIntegrationTokenRoundTrips(t *testing.T) {
	signer := integrationSigner(t)
	expires := time.Now().Add(security.IntegrationTokenTTL)
	token, err := signer.MintIntegration(security.IntegrationClaims{
		Subject: "int_abcdefghijklmnopqrstuvwxyz", SpaceID: "space-1",
		Access: []string{"rooms.read", "rooms.write"}, KeyPrefix: "app:board:", Name: "Deploy bot",
	}, expires)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := signer.VerifyIntegration(token)
	if err != nil {
		t.Fatalf("VerifyIntegration() error = %v", err)
	}
	if claims.Subject != "int_abcdefghijklmnopqrstuvwxyz" || claims.SpaceID != "space-1" || claims.KeyPrefix != "app:board:" ||
		claims.Name != "Deploy bot" || len(claims.Access) != 2 || claims.Access[1] != "rooms.write" {
		t.Fatalf("claims = %#v", claims)
	}
	if claims.ExpiresAt.Unix() != expires.Unix() {
		t.Fatalf("expires = %v, want %v", claims.ExpiresAt, expires)
	}
}

func TestIntegrationTokenIsNeverAPersonsToken(t *testing.T) {
	signer := integrationSigner(t)
	token, err := signer.MintIntegration(security.IntegrationClaims{
		Subject: "int_abcdefghijklmnopqrstuvwxyz", SpaceID: "space-1", Access: []string{"rooms.read"}, Name: "Bot",
	}, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"access", "refresh"} {
		if _, err := signer.Verify(token, kind); err == nil {
			t.Fatalf("integration token accepted as %s", kind)
		}
	}
	request := httptest.NewRequest("GET", "/v1/me", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	if user, err := accounts.SessionUserID(request); err != nil || user != "" {
		t.Fatalf("SessionUserID() = %q, %v; an integration must never sign in as a person", user, err)
	}
	if sid := accounts.SessionID(request); sid != "" {
		t.Fatalf("SessionID() = %q for an integration token", sid)
	}
}

func TestPersonsTokenIsNeverAnIntegration(t *testing.T) {
	signer := integrationSigner(t)
	for _, kind := range []string{"access", "refresh"} {
		token, err := signer.Mint("user", "session", kind, time.Now().Add(time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := signer.VerifyIntegration(token); err == nil {
			t.Fatalf("%s token accepted as an integration", kind)
		}
	}
	if _, err := signer.MintIntegration(security.IntegrationClaims{Subject: "int_x", SpaceID: "s"}, time.Now().Add(time.Minute)); err == nil {
		t.Fatal("an integration token with no access was minted")
	}
}

func TestSessionPublicIDFollowsTheSession(t *testing.T) {
	id := security.SessionPublicID("sid-1")
	if id != security.SessionPublicIDFromHash(security.HashToken("sid-1")) || !strings.HasPrefix(id, "ses_") || len(id) != 36 {
		t.Fatalf("SessionPublicID() = %q", id)
	}
	if id == security.SessionPublicID("sid-2") {
		t.Fatal("two sessions share a public id")
	}
}
