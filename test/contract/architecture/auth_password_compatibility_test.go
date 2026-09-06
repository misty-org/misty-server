package architecture

import (
	"encoding/json"
	"os"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestNodeAndGoPasswordHashCompatibility(t *testing.T) {
	raw, err := os.ReadFile("../../../docs/migration/fixtures/auth-passwords.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []struct {
		Password        string `json:"password"`
		Hash            string `json:"hash"`
		ExtendedMatches bool   `json:"extendedMatches"`
	}
	if err := json.Unmarshal(raw, &fixtures); err != nil {
		t.Fatal(err)
	}
	for _, fixture := range fixtures {
		if err := bcrypt.CompareHashAndPassword([]byte(fixture.Hash), []byte(fixture.Password)); err != nil {
			t.Fatalf("cross-runtime hash rejected: %v", err)
		}
		if actual := bcrypt.CompareHashAndPassword([]byte(fixture.Hash), []byte(fixture.Password+"suffix")) == nil; actual != fixture.ExtendedMatches {
			t.Fatal("cross-runtime byte boundary behavior differs")
		}
	}
}
