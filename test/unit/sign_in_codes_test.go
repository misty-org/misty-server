package unit

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/misty-org/misty-server/internal/accounts"
	"github.com/misty-org/misty-server/internal/spaces"
)

func TestUserCodesUseTheAlphabetAndReadBack(t *testing.T) {
	seen := map[string]bool{}
	for range 200 {
		code, err := accounts.NewUserCode()
		if err != nil {
			t.Fatal(err)
		}
		if len(code) != accounts.UserCodeLength {
			t.Fatalf("code %q is not %d letters", code, accounts.UserCodeLength)
		}
		for _, r := range code {
			if !strings.ContainsRune(accounts.UserCodeAlphabet, r) {
				t.Fatalf("code %q has %q outside the alphabet", code, r)
			}
		}
		shown := accounts.FormatUserCode(code)
		if len(shown) != 9 || shown[4] != '-' {
			t.Fatalf("shown as %q", shown)
		}
		back, ok := accounts.NormalizeUserCode(strings.ToLower(shown))
		if !ok || back != code {
			t.Fatalf("NormalizeUserCode(%q) = %q, %v", shown, back, ok)
		}
		seen[code] = true
	}
	if len(seen) < 190 {
		t.Fatalf("only %d distinct codes in 200", len(seen))
	}
	for _, bad := range []string{"", "WDJB-MJH", "WDJB-MJHTB", "WDJA-MJHT", "WDJB_MJHT", "WDJ1MJHT"} {
		if _, ok := accounts.NormalizeUserCode(bad); ok {
			t.Fatalf("NormalizeUserCode(%q) accepted", bad)
		}
	}
	if link := accounts.DeviceVerificationLink("WDJBMJHT"); link != "misty://device?code=WDJB-MJHT" {
		t.Fatalf("link = %q", link)
	}
}

func TestDeviceSignInIDsAndNames(t *testing.T) {
	id, err := accounts.NewDeviceSignInID()
	if err != nil || !accounts.ValidDeviceSignInID(id) {
		t.Fatalf("NewDeviceSignInID() = %q, %v", id, err)
	}
	if accounts.ValidDeviceSignInID("dsi_short") || accounts.ValidDeviceSignInID("lookup") {
		t.Fatal("a malformed id passed")
	}
	if name, ok := accounts.NormalizeDeviceName("  misty-cli on studio-mac "); !ok || name != "misty-cli on studio-mac" {
		t.Fatalf("NormalizeDeviceName() = %q, %v", name, ok)
	}
	for _, bad := range []string{"", "   ", "line\nbreak", strings.Repeat("a", 65)} {
		if _, ok := accounts.NormalizeDeviceName(bad); ok {
			t.Fatalf("NormalizeDeviceName(%q) accepted", bad)
		}
	}
	if accounts.AppSessionName("studio-mac") != "Misty on studio-mac" || accounts.AppSessionName("") != "Misty" {
		t.Fatal("AppSessionName does not name the host")
	}
}

func TestIntegrationTokensAreRecognizable(t *testing.T) {
	seen := map[string]bool{}
	for range 100 {
		token, hash, err := spaces.NewIntegrationToken()
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(token, "misty_int_") || len(token) != len("misty_int_")+43 || !spaces.ValidIntegrationToken(token) {
			t.Fatalf("token %q", token)
		}
		if hash != spaces.HashIntegrationToken(token) || len(hash) != 64 || strings.Contains(hash, token) {
			t.Fatalf("hash %q", hash)
		}
		seen[token] = true
	}
	if len(seen) != 100 {
		t.Fatal("tokens repeat")
	}
	if spaces.ValidIntegrationToken("misty_int_short") || spaces.ValidIntegrationToken("misty_int_"+strings.Repeat("-", 43)) {
		t.Fatal("a malformed token passed")
	}
	id, err := spaces.NewIntegrationID()
	if err != nil || !spaces.ValidIntegrationID(id) {
		t.Fatalf("NewIntegrationID() = %q, %v", id, err)
	}
}

func TestIntegrationInputRules(t *testing.T) {
	if access, err := spaces.NormalizeIntegrationAccess([]string{"rooms.write", "rooms.read", "rooms.write"}); err != nil ||
		len(access) != 2 || access[0] != "rooms.read" {
		t.Fatalf("NormalizeIntegrationAccess() = %v, %v", access, err)
	}
	for _, bad := range [][]string{nil, {}, {"administrator"}, {"rooms.read", "integrations.manage"}, {"roles.manage"}} {
		if _, err := spaces.NormalizeIntegrationAccess(bad); err == nil {
			t.Fatalf("access %v accepted", bad)
		}
	}
	if _, err := spaces.NormalizeIntegrationKeyPrefix(strings.Repeat("k", 257)); err == nil {
		t.Fatal("a 257 byte key prefix passed")
	}
	if _, err := spaces.NormalizeIntegrationKeyPrefix("app:\x01"); err == nil {
		t.Fatal("a key prefix with a control character passed")
	}
	if avatar, err := spaces.NormalizeIntegrationAvatar(nil); err != nil || string(avatar) != "{}" {
		t.Fatalf("empty avatar = %s, %v", avatar, err)
	}
	if _, err := spaces.NormalizeIntegrationAvatar(json.RawMessage(`{"nested":{"a":1}}`)); err == nil {
		t.Fatal("a nested avatar passed")
	}
	if _, err := spaces.NormalizeIntegrationName(" "); err == nil {
		t.Fatal("an empty name passed")
	}
}
