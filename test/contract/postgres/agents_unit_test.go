package db

import (
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestWorkflowNodeLeaseTokensAreOpaqueAndHashed(t *testing.T) {
	token, err := TestingSecureToken()
	if err != nil {
		t.Fatalf("secureToken() error = %v", err)
	}
	if len(token) < 40 {
		t.Fatalf("lease token length = %d, want at least 40", len(token))
	}
	hash := TestingHashToken(token)
	if hash == token || len(hash) != 64 {
		t.Fatalf("hashToken() returned unsafe digest %q", hash)
	}
	if hash != TestingHashToken(token) {
		t.Fatal("lease token hashing is not deterministic")
	}
}
