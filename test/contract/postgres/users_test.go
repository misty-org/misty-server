package db

import (
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
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

func TestCreateUserNormalizesEmailAndCreatesLicense(t *testing.T) {
	database := openTestDatabase(t)

	user, err := database.CreateUser("Ada", "  Ada@Example.com ", "password123")
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	if user.Email != "ada@example.com" {
		t.Fatalf("user.Email = %q, want %q", user.Email, "ada@example.com")
	}
	if user.Username != "ada" {
		t.Fatalf("user.Username = %q, want %q", user.Username, "ada")
	}

	license, err := database.GetLicenseByUserID(user.ID)
	if err != nil || license == nil {
		t.Fatalf("GetLicenseByUserID() error = %v, license = %#v", err, license)
	}
	if license.ID != user.LicenseID {
		t.Fatalf("license.ID = %q, want %q", license.ID, user.LicenseID)
	}

	if _, err := database.CreateUser("Ada Duplicate", "ada@example.com", "password123"); err == nil {
		t.Fatal("CreateUser() succeeded for duplicate email")
	}
}

func TestUserSettingsDefaultAndUpdate(t *testing.T) {
	database := openTestDatabase(t)

	user, err := database.CreateUser("Ada", "ada@example.com", "password123")
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
	user, err := database.CreateUser("Avatar User", "avatar@example.com", "password123")
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}

	// Avatar bytes live in object storage (R2); Postgres tracks only the version.
	version, err := database.GetUserAvatarVersion(user.ID)
	if err != nil || version != 0 {
		t.Fatalf("GetUserAvatarVersion() before upload = %d, %v; want 0, nil", version, err)
	}

	version, err = database.BumpUserAvatarVersion(user.ID)
	if err != nil || version != 1 {
		t.Fatalf("BumpUserAvatarVersion() = %d, %v; want 1, nil", version, err)
	}
	version, err = database.GetUserAvatarVersion(user.ID)
	if err != nil || version != 1 {
		t.Fatalf("GetUserAvatarVersion() = %d, %v; want 1, nil", version, err)
	}
}
