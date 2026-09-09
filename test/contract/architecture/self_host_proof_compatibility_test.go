package architecture

import (
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/kannachi323/misty/server/internal/platform/entitlement"
)

func TestNodeAndGoSelfHostProofCompatibility(t *testing.T) {
	raw, err := os.ReadFile("../../../test/fixtures/compatibility/self-host-proofs.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Now, KeyID, PublicKey, SubjectSecret, UserID, NodeToken, GoToken, ExpiresAt, Subject string
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	key, err := base64.StdEncoding.DecodeString(fixture.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	secret, err := base64.StdEncoding.DecodeString(fixture.SubjectSecret)
	if err != nil {
		t.Fatal(err)
	}
	now, err := time.Parse(time.RFC3339, fixture.Now)
	if err != nil {
		t.Fatal(err)
	}
	expiresAt, err := time.Parse(time.RFC3339, fixture.ExpiresAt)
	if err != nil {
		t.Fatal(err)
	}
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(fixture.UserID))
	expected := "license_" + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	keys := map[string]ed25519.PublicKey{fixture.KeyID: key}
	for _, token := range []string{fixture.NodeToken, fixture.GoToken} {
		claims, err := entitlement.Verify(token, keys, now)
		if err != nil {
			t.Fatal(err)
		}
		if claims.Subject != expected || claims.Subject != fixture.Subject || !time.Unix(claims.ExpiresAt, 0).Equal(expiresAt) {
			t.Fatal("cross-runtime account binding or expiry differs")
		}
		if _, err := entitlement.Verify(token, keys, expiresAt); err != entitlement.ErrExpired {
			t.Fatal("expired proof accepted")
		}
	}
}
