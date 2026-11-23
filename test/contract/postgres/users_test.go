package db

import (
	"testing"

	. "github.com/misty-org/misty-server/internal/platform/db"
)

func TestNormalizeUsername(t *testing.T) {
	for input, want := range map[string]string{
		"Misty_User":    "misty_user",
		"  mtccool668 ": "mtccool668",
	} {
		got, err := TestingNormalizeUsername(input)
		if err != nil || got != want {
			t.Fatalf("normalizeUsername(%q) = %q, %v; want %q", input, got, err, want)
		}
	}
	for _, input := range []string{"ab", "has-dash", "has space", "猫猫猫", "this_username_is_more_than_thirty_characters"} {
		if _, err := TestingNormalizeUsername(input); err == nil {
			t.Fatalf("normalizeUsername(%q) succeeded, want validation error", input)
		}
	}
}

func TestUserSettingsDefaultAndUpdate(t *testing.T) {
	database := openTestDatabase(t)

	user, err := database.CreateUser("ada@example.com", "password123")
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}

	settings, err := database.GetUserSettingsByID(user.ID)
	if err != nil {
		t.Fatalf("GetUserSettingsByID() error = %v", err)
	}
	if settings == nil {
		t.Fatal("GetUserSettingsByID() returned nil settings")
	}
	if settings.EmailUpdatesEnabled {
		t.Fatal("EmailUpdatesEnabled = true, want false")
	}
	if settings.AnalyticsEnabled || settings.ErrorReportingEnabled {
		t.Fatalf("telemetry defaults = %#v, want disabled", settings)
	}

	if err := database.UpdateUserSettings(user.ID, UserSettings{EmailUpdatesEnabled: true, AnalyticsEnabled: true, ErrorReportingEnabled: true}); err != nil {
		t.Fatalf("UpdateUserSettings() error = %v", err)
	}

	settings, err = database.GetUserSettingsByID(user.ID)
	if err != nil {
		t.Fatalf("GetUserSettingsByID() after update error = %v", err)
	}
	if settings == nil || !settings.EmailUpdatesEnabled || !settings.AnalyticsEnabled || !settings.ErrorReportingEnabled {
		t.Fatalf("settings after update = %#v, want all preferences enabled", settings)
	}
}

func TestUserAvatarVersionBumps(t *testing.T) {
	database := openTestDatabase(t)
	user, err := database.CreateUser("avatar@example.com", "password123")
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}

	avatar, err := database.GetUserAvatarReference(user.ID)
	if err != nil || avatar.Version != 0 || avatar.ObjectKey != "avatars/"+user.ID {
		t.Fatalf("reference before upload = %#v, %v; want version 0 and the fallback key", avatar, err)
	}

	first := "avatars/avatar_12345678-1234-1234-1234-123456789abc"
	version, err := database.BumpUserAvatarVersion(user.ID, first)
	if err != nil || version <= 0 {
		t.Fatalf("BumpUserAvatarVersion() = %d, %v; want a positive version", version, err)
	}
	avatar, err = database.GetUserAvatarReference(user.ID)
	if err != nil || avatar.Version != version || avatar.ObjectKey != first {
		t.Fatalf("reference after upload = %#v, %v; want version %d and key %s", avatar, err, version, first)
	}
	if got, err := database.GetUserAvatarVersion(user.ID); err != nil || got != version {
		t.Fatalf("GetUserAvatarVersion() = %d, %v; want %d", got, err, version)
	}

	second := "avatars/avatar_87654321-4321-4321-4321-cba987654321"
	next, err := database.BumpUserAvatarVersion(user.ID, second)
	if err != nil || next <= version {
		t.Fatalf("second BumpUserAvatarVersion() = %d, %v; want more than %d", next, err, version)
	}
	avatar, err = database.GetUserAvatarReference(user.ID)
	if err != nil || avatar.Version != next || avatar.ObjectKey != second {
		t.Fatalf("reference after second upload = %#v, %v; want version %d and key %s", avatar, err, next, second)
	}
}
