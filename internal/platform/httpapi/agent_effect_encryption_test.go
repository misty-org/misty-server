package api

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/json"
	"testing"
)

func TestEffectReplayEncryptionBindsIdentity(t *testing.T) {
	block, err := aes.NewCipher(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	service := &SpacesService{aead: aead}
	raw := json.RawMessage(`{"message_id":"sent-once","private":"secret"}`)
	encrypted, err := service.protectAgentEffectResult("run:a:effect:b", raw)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encrypted, []byte("secret")) {
		t.Fatal("plaintext replay")
	}
	restored, err := service.restoreAgentEffectResult("run:a:effect:b", encrypted)
	if err != nil || !bytes.Equal(restored, raw) {
		t.Fatalf("replay: %s, %v", restored, err)
	}
	if _, err := service.restoreAgentEffectResult("run:other:effect:b", encrypted); err == nil {
		t.Fatal("cross-effect ciphertext accepted")
	}
}
