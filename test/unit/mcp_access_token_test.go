package unit

import (
	"bytes"
	"strings"
	"testing"
	"time"

	api "github.com/kannachi323/misty/server/internal/platform/httpapi"
)

func TestMCPAccessTokenBindsRunRuntimeAndSubject(t *testing.T) {
	secret := bytes.Repeat([]byte{7}, 32)
	now := time.Now().UTC()
	token, err := api.TestingSignMCPAccessToken(secret, "user_1", "run_1", "workflow_1", "token_1", "", now, now.Add(5*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	subject, runID, runtimeRunID, tokenID, err := api.TestingVerifyMCPAccessToken(token, secret)
	if err != nil {
		t.Fatal(err)
	}
	if subject != "user_1" || runID != "run_1" || runtimeRunID != "workflow_1" || tokenID != "token_1" {
		t.Fatalf("claims = %q %q %q %q", subject, runID, runtimeRunID, tokenID)
	}
}

func TestMCPAccessTokenSupportsRotationAndRejectsTampering(t *testing.T) {
	current := bytes.Repeat([]byte{3}, 32)
	previous := bytes.Repeat([]byte{5}, 32)
	now := time.Now().UTC()
	token, err := api.TestingSignMCPAccessToken(previous, "user_1", "run_1", "workflow_1", "token_1", "", now, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := api.TestingVerifyMCPAccessToken(token, current, previous); err != nil {
		t.Fatalf("rotation secret should verify: %v", err)
	}
	parts := strings.Split(token, ".")
	parts[1] = parts[1][:len(parts[1])-1] + "A"
	if _, _, _, _, err := api.TestingVerifyMCPAccessToken(strings.Join(parts, "."), current, previous); err == nil {
		t.Fatal("tampered token should be rejected")
	}
}

func TestMCPAccessTokenRejectsExpiredAndWrongAudience(t *testing.T) {
	secret := bytes.Repeat([]byte{9}, 32)
	now := time.Now().UTC()
	tests := []struct {
		name, audience  string
		issued, expires time.Time
	}{
		{name: "expired", issued: now.Add(-2 * time.Minute), expires: now.Add(-time.Minute)},
		{name: "wrong audience", audience: "another-service", issued: now, expires: now.Add(time.Minute)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			token, err := api.TestingSignMCPAccessToken(secret, "user_1", "run_1", "workflow_1", "token_1", test.audience, test.issued, test.expires)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, _, _, err := api.TestingVerifyMCPAccessToken(token, secret); err == nil {
				t.Fatal("invalid claims should be rejected")
			}
		})
	}
}
