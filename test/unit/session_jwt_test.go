package unit

import (
	"encoding/base64"
	"github.com/golang-jwt/jwt/v5"
	"github.com/kannachi323/misty/server/internal/platform/security"
	"strings"
	"testing"
	"time"
)

func TestSessionJWTValidation(t *testing.T) {
	key := []byte(strings.Repeat("a", 32))
	t.Setenv("MISTY_AUTH_SIGNING_KEY", base64.StdEncoding.EncodeToString(key))
	t.Setenv("MISTY_AUTH_SIGNING_KEY_PREVIOUS", "")
	signer, err := security.SessionSignerFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	valid, err := signer.Mint("user", "session", "access", time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	claims, err := signer.Verify(valid, "access")
	if err != nil || claims.Subject != "user" {
		t.Fatalf("valid token rejected: %v", err)
	}
	if _, err := signer.Verify(valid, "refresh"); err == nil {
		t.Fatal("access token accepted as refresh")
	}
	for _, mutate := range []string{"expired", "issuer", "audience", "subject", "sid", "jti", "issued", "algorithm"} {
		t.Run(mutate, func(t *testing.T) {
			parsed, _, err := jwt.NewParser().ParseUnverified(valid, &security.SessionClaims{})
			if err != nil {
				t.Fatal(err)
			}
			c := parsed.Claims.(*security.SessionClaims)
			switch mutate {
			case "expired":
				c.ExpiresAt = jwt.NewNumericDate(time.Now().Add(-time.Minute))
			case "issuer":
				c.Issuer = "other"
			case "audience":
				c.Audience = jwt.ClaimStrings{"other"}
			case "subject":
				c.Subject = ""
			case "sid":
				c.SessionID = ""
			case "jti":
				c.ID = ""
			case "issued":
				c.IssuedAt = nil
			case "algorithm":
				parsed.Method = jwt.SigningMethodHS384
			}
			raw, err := parsed.SignedString(key)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := signer.Verify(raw, "access"); err == nil {
				t.Fatal("invalid token accepted")
			}
		})
	}
	if _, err := signer.Verify(valid+"corrupt", "access"); err == nil {
		t.Fatal("tampered signature accepted")
	}
}

func TestSessionJWTSigningKeyRotationAndConfiguration(t *testing.T) {
	old := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("a", 32)))
	t.Setenv("MISTY_AUTH_SIGNING_KEY", old)
	t.Setenv("MISTY_AUTH_SIGNING_KEY_PREVIOUS", "")
	signer, _ := security.SessionSignerFromEnv()
	raw, _ := signer.Mint("user", "sid", "refresh", time.Now().Add(time.Hour))
	t.Setenv("MISTY_AUTH_SIGNING_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("b", 32))))
	t.Setenv("MISTY_AUTH_SIGNING_KEY_PREVIOUS", old)
	rotated, _ := security.SessionSignerFromEnv()
	if _, err := rotated.Verify(raw, "refresh"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MISTY_AUTH_SIGNING_KEY_PREVIOUS", "")
	rotated, _ = security.SessionSignerFromEnv()
	if _, err := rotated.Verify(raw, "refresh"); err == nil {
		t.Fatal("retired key accepted")
	}
	t.Setenv("MISTY_ENVIRONMENT", "production")
	for _, key := range []string{"", "invalid-base64", base64.StdEncoding.EncodeToString([]byte("short"))} {
		t.Setenv("MISTY_AUTH_SIGNING_KEY", key)
		if _, err := security.SessionSignerFromEnv(); err == nil {
			t.Fatal("invalid production configuration accepted")
		}
	}
}
