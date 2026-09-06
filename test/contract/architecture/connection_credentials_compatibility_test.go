package architecture

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"os"
	"testing"
)

func TestNodeAndGoConnectionCredentialCompatibility(t *testing.T) {
	raw, err := os.ReadFile("../../../docs/migration/fixtures/connection-credentials.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []struct{ Provider, Key, Nonce, Ciphertext, Plaintext string }
	if err := json.Unmarshal(raw, &fixtures); err != nil {
		t.Fatal(err)
	}
	decode := func(value string) []byte {
		t.Helper()
		result, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	for _, fixture := range fixtures {
		block, err := aes.NewCipher(decode(fixture.Key))
		if err != nil {
			t.Fatal(err)
		}
		aead, err := cipher.NewGCM(block)
		if err != nil {
			t.Fatal(err)
		}
		nonce, encrypted, aad := decode(fixture.Nonce), decode(fixture.Ciphertext), []byte("misty-connected-account-v1:"+fixture.Provider)
		plaintext, err := aead.Open(nil, nonce, encrypted, aad)
		if err != nil || string(plaintext) != fixture.Plaintext {
			t.Fatalf("Node credential incompatible with Go: %v", err)
		}
		if !bytes.Equal(aead.Seal(nil, nonce, []byte(fixture.Plaintext), aad), encrypted) {
			t.Fatal("Go credential incompatible with Node fixture")
		}
		if _, err := aead.Open(nil, nonce, encrypted, []byte("misty-connected-account-v1:wrong")); err == nil {
			t.Fatal("provider binding was ignored")
		}
	}
}

func TestNodeAndGoLegacyProviderCredentialCompatibility(t *testing.T) {
	raw, err := os.ReadFile("../../../docs/migration/fixtures/legacy-provider-credentials.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []struct{ Provider, AadProvider, Key, Nonce, Ciphertext, Plaintext string }
	if err := json.Unmarshal(raw, &fixtures); err != nil {
		t.Fatal(err)
	}
	decode := func(value string) []byte {
		t.Helper()
		result, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	for _, fixture := range fixtures {
		block, err := aes.NewCipher(decode(fixture.Key))
		if err != nil {
			t.Fatal(err)
		}
		aead, err := cipher.NewGCM(block)
		if err != nil {
			t.Fatal(err)
		}
		nonce, encrypted := decode(fixture.Nonce), decode(fixture.Ciphertext)
		aad := []byte("misty-provider-v2:" + fixture.Provider)
		plaintext, err := aead.Open(nil, nonce, encrypted, aad)
		if err != nil && fixture.Provider == "google" {
			aad = []byte("misty-provider-v2:google_calendar")
			plaintext, err = aead.Open(nil, nonce, encrypted, aad)
		}
		if err != nil || string(plaintext) != fixture.Plaintext {
			t.Fatalf("legacy Node credential incompatible with Go: %v", err)
		}
		if string(aad) != "misty-provider-v2:"+fixture.AadProvider || !bytes.Equal(aead.Seal(nil, nonce, []byte(fixture.Plaintext), aad), encrypted) {
			t.Fatal("legacy Go credential incompatible with Node fixture")
		}
		for _, wrong := range []string{"misty-provider-v2:wrong", "misty-connected-account-v1:" + fixture.Provider} {
			if _, err := aead.Open(nil, nonce, encrypted, []byte(wrong)); err == nil {
				t.Fatal("provider or credential-format binding was ignored")
			}
		}
	}
}
