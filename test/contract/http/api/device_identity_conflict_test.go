package api

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"testing"
)

// Registration proves possession of the device key, is idempotent for the same
// key, and needs a signed-in session.
func TestDeviceRegistrationProvesItsKey(t *testing.T) {
	f := newDeviceFixture(t)
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	if response := f.registerKey(key, "Device", false); response.Code != http.StatusBadRequest {
		t.Fatalf("a proof from another key was accepted: %d", response.Code)
	}
	first := f.registerKey(key, "Device", true)
	again := f.registerKey(key, "Device again", true)
	var a, b struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(first.Body.Bytes(), &a)
	_ = json.Unmarshal(again.Body.Bytes(), &b)
	if first.Code != http.StatusCreated || again.Code != http.StatusCreated || a.ID == "" || a.ID != b.ID {
		t.Fatalf("re-registering the same key: %d %d %q %q", first.Code, again.Code, a.ID, b.ID)
	}
	token := f.token
	f.token = ""
	if response := f.registerKey(key, "Device", true); response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated registration: %d", response.Code)
	}
	f.token = token
}
