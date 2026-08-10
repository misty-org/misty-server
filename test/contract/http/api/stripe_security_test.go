package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	. "github.com/kannachi323/misty/server/internal/platform/httpapi"
)

// signStripePayload produces the signature header Stripe would send for a
// given secret. With an empty secret this is exactly what any attacker can
// compute, which is the case these tests exist to prevent.
func signStripePayload(t *testing.T, payload, secret string) string {
	t.Helper()
	timestamp := time.Now().Unix()
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(fmt.Sprintf("%d.%s", timestamp, payload)))
	return fmt.Sprintf("t=%d,v1=%s", timestamp, hex.EncodeToString(mac.Sum(nil)))
}

func postStripeWebhook(t *testing.T, secret, payload, signature string) int {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/stripe/webhook", strings.NewReader(payload))
	request.Header.Set("Stripe-Signature", signature)
	recorder := httptest.NewRecorder()
	// A nil service is safe here: every case must be rejected before dispatch.
	StripeWebhookWithService(secret, nil).ServeHTTP(recorder, request)
	return recorder.Code
}

const stripeTestPayload = `{"id":"evt_forged","type":"checkout.session.completed","data":{"object":{}}}`

func TestStripeWebhookRefusesEventsWhenSecretIsUnset(t *testing.T) {
	// An empty secret is a usable HMAC key, so a forged event would otherwise
	// verify and grant paid tiers or credits for free.
	code := postStripeWebhook(t, "", stripeTestPayload, signStripePayload(t, stripeTestPayload, ""))
	if code != http.StatusServiceUnavailable {
		t.Fatalf("forged event with an unset secret returned %d, want 503", code)
	}
}

func TestStripeWebhookRefusesPlaceholderSecrets(t *testing.T) {
	for _, secret := range []string{"   ", "changeme", "whsec_x"} {
		code := postStripeWebhook(t, secret, stripeTestPayload, signStripePayload(t, stripeTestPayload, secret))
		if code != http.StatusServiceUnavailable {
			t.Fatalf("secret %q returned %d, want 503", secret, code)
		}
	}
}

func TestStripeWebhookRejectsAForgedSignature(t *testing.T) {
	realSecret := "whsec_realsecretvalue_abcdef123456"
	// The attacker signs with their own secret; verification must fail.
	forged := signStripePayload(t, stripeTestPayload, "whsec_attackerguess_abcdef123456")
	if code := postStripeWebhook(t, realSecret, stripeTestPayload, forged); code != http.StatusBadRequest {
		t.Fatalf("forged signature returned %d, want 400", code)
	}
}

func TestStripeWebhookRejectsAMissingSignature(t *testing.T) {
	realSecret := "whsec_realsecretvalue_abcdef123456"
	if code := postStripeWebhook(t, realSecret, stripeTestPayload, ""); code != http.StatusBadRequest {
		t.Fatalf("missing signature returned %d, want 400", code)
	}
}

func TestStripeWebhookRejectsAStaleTimestamp(t *testing.T) {
	realSecret := "whsec_realsecretvalue_abcdef123456"
	// Replaying a captured event hours later must fail Stripe's tolerance check.
	stale := time.Now().Add(-24 * time.Hour).Unix()
	mac := hmac.New(sha256.New, []byte(realSecret))
	mac.Write([]byte(fmt.Sprintf("%d.%s", stale, stripeTestPayload)))
	signature := fmt.Sprintf("t=%d,v1=%s", stale, hex.EncodeToString(mac.Sum(nil)))

	if code := postStripeWebhook(t, realSecret, stripeTestPayload, signature); code != http.StatusBadRequest {
		t.Fatalf("replayed event returned %d, want 400", code)
	}
}
