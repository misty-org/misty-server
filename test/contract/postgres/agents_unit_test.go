package db

import (
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/db"
)

func TestWorkflowNodeLeaseTokensAreOpaqueAndHashed(t *testing.T) {
	token, err := SecureToken()
	if err != nil {
		t.Fatalf("secureToken() error = %v", err)
	}
	if len(token) < 40 {
		t.Fatalf("lease token length = %d, want at least 40", len(token))
	}
	hash := HashToken(token)
	if hash == token || len(hash) != 64 {
		t.Fatalf("hashToken() returned unsafe digest %q", hash)
	}
	if hash != HashToken(token) {
		t.Fatal("lease token hashing is not deterministic")
	}
}
