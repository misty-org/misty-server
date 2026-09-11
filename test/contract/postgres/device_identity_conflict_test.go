package db

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestDeviceRegistrationIdentityConflictWithRuntimeRole(t *testing.T) {
	admin := openTestDatabase(t)
	owner, err := admin.CreateUser("Device owner", "device-owner@example.invalid", "password123")
	if err != nil {
		t.Fatal(err)
	}
	other, err := admin.CreateUser("Other owner", "device-other@example.invalid", "password123")
	if err != nil {
		t.Fatal(err)
	}
	runtime := openRuntimeRoleDatabase(t, admin)
	keyFor := func(value string) string {
		key := sha256.Sum256([]byte(value))
		return base64.RawURLEncoding.EncodeToString(key[:])
	}
	register := func(user, key, endpoint string) (*TrustedDevice, error) {
		return runtime.RegisterTrustedDevice(user, "Device", keyFor(key), "macos", endpoint, json.RawMessage(`["misty-device/1"]`), json.RawMessage(`{}`))
	}
	endpoint := strings.Repeat("a", 64)
	first, err := register(owner.ID, "first-key", endpoint)
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := register(owner.ID, "first-key", endpoint)
	if err != nil || repeated.ID != first.ID {
		t.Fatalf("idempotent registration: %v %v", repeated, err)
	}
	if _, err = register(owner.ID, "different-key", endpoint); !errors.Is(err, ErrDeviceIdentityConflict) {
		t.Fatalf("endpoint conflict: %v", err)
	}
	second, err := register(owner.ID, "second-key", strings.Repeat("b", 64))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = register(owner.ID, "second-key", endpoint); !errors.Is(err, ErrDeviceIdentityConflict) {
		t.Fatalf("update collision: %v", err)
	}
	if _, err = register(other.ID, "different-key", endpoint); err != nil {
		t.Fatalf("another account blocked: %v", err)
	}
	if err = runtime.RevokeTrustedDevice(other.ID, first.ID); !errors.Is(err, ErrDeviceNotFound) {
		t.Fatalf("other account revoked device: %v", err)
	}
	if err = runtime.RevokeTrustedDevice(owner.ID, first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = register(owner.ID, "different-key", endpoint); !errors.Is(err, ErrDeviceIdentityConflict) {
		t.Fatalf("revoked identity overwritten: %v", err)
	}
	devices, err := runtime.TrustedDevices(owner.ID)
	if err != nil || len(devices) != 2 {
		t.Fatalf("device records changed: %v %v", devices, err)
	}
	for _, d := range devices {
		switch d.ID {
		case first.ID:
			if d.PublicKey != keyFor("first-key") || d.P2PEndpointID != endpoint || d.RevokedAt == nil {
				t.Fatalf("original identity changed: %#v", d)
			}
		case second.ID:
			if d.PublicKey != keyFor("second-key") || d.P2PEndpointID != strings.Repeat("b", 64) {
				t.Fatalf("failed update was not atomic: %#v", d)
			}
		default:
			t.Fatal("unexpected device")
		}
	}
	// Independent signing identities race for the same endpoint. Exactly one wins.
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, key := range []string{"race-one", "race-two"} {
		go func(key string) { <-start; _, err := register(owner.ID, key, strings.Repeat("c", 64)); results <- err }(key)
	}
	close(start)
	successes, conflicts := 0, 0
	for range 2 {
		err := <-results
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrDeviceIdentityConflict):
			conflicts++
		default:
			t.Fatal(err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("race: %d successes, %d conflicts", successes, conflicts)
	}
}
