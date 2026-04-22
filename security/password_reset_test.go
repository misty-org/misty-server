package security

import "testing"

func TestGenerateSecureToken(t *testing.T) {
	tokenA, err := GenerateSecureToken()
	if err != nil {
		t.Fatalf("GenerateSecureToken() error = %v", err)
	}

	tokenB, err := GenerateSecureToken()
	if err != nil {
		t.Fatalf("GenerateSecureToken() second call error = %v", err)
	}

	if len(tokenA) < 43 {
		t.Fatalf("token length = %d, want at least 43", len(tokenA))
	}
	if tokenA == tokenB {
		t.Fatal("GenerateSecureToken() returned duplicate tokens")
	}
}

func TestHashToken(t *testing.T) {
	hash := HashToken("reset-token")
	if hash != "7c18b43a1d8227cddb332e67971e790ce35ac2303f4fccfb2a565622f2fe1cec" {
		t.Fatalf("HashToken() = %q", hash)
	}
}
