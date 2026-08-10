package db

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"golang.org/x/crypto/bcrypt"
)

var (
	ErrInvalidUsername = errors.New("username must be 3-30 lowercase letters, numbers, or underscores")
	ErrUsernameTaken   = errors.New("username already taken")
)

type User struct {
	ID                  string
	LicenseID           string
	Name                string
	Username            string
	Email               string
	AvatarVersion       int64
	EmailUpdatesEnabled bool
	CreatedAt           time.Time
}

type UserSettings struct {
	EmailUpdatesEnabled   bool `json:"email_updates_enabled"`
	AnalyticsEnabled      bool `json:"analytics_enabled"`
	ErrorReportingEnabled bool `json:"error_reporting_enabled"`
}

func (db *Database) CreateUser(name, email, password string) (*User, error) {
	return db.createUser(name, defaultUsernameForEmail(email), email, password)
}

func (db *Database) CreateUserWithUsername(name, username, email, password string) (*User, error) {
	normalizedUsername, err := TestingNormalizeUsername(username)
	if err != nil {
		return nil, err
	}
	return db.createUser(name, normalizedUsername, email, password)
}

func (db *Database) createUser(name, username, email, password string) (*User, error) {
	normalizedEmail := normalizeEmail(email)
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}

	id := uuid.New().String()
	now := time.Now()
	licenseID := uuid.New().String()

	var license *License
	err = db.TestingWithRLSContext(context.Background(), registrationRLSSettings(id, licenseID, normalizedEmail), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(
			context.Background(),
			`INSERT INTO users (id, license_id, name, username, email, password_hash) VALUES ($1, $2, $3, $4, $5, $6)`,
			id, licenseID, name, username, normalizedEmail, hash,
		)
		if err != nil {
			return err
		}

		license, err = createLicenseTx(tx, licenseID, id, TierBasic, LicenseStatusActive, nil)
		return err
	})
	if err != nil {
		var pqError *pq.Error
		if errors.As(err, &pqError) && pqError.Constraint == "users_username_unique_idx" {
			return nil, ErrUsernameTaken
		}
		log.Println("Failed to create user:", err)
		return nil, err
	}

	return &User{ID: id, LicenseID: license.ID, Name: name, Username: username, Email: normalizedEmail, CreatedAt: now}, nil
}

func (db *Database) GetUserByEmail(email string) (*User, string, error) {
	var u User
	var hash string
	normalizedEmail := normalizeEmail(email)

	err := db.TestingWithRLSContext(context.Background(), anonymousRLSSettings(normalizedEmail), func(tx *sql.Tx) error {
		return tx.QueryRowContext(
			context.Background(),
			`SELECT id, license_id, name, username, email, password_hash, created_at
			 FROM users WHERE LOWER(email)=$1 AND lifecycle_state='active'`,
			normalizedEmail,
		).Scan(&u.ID, &u.LicenseID, &u.Name, &u.Username, &u.Email, &hash, &u.CreatedAt)
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, "", nil
		}
		log.Println("Failed to get user:", err)
		return nil, "", err
	}

	return &u, hash, nil
}

func (db *Database) UpdateUserName(id, name string) error {
	err := db.TestingWithRLSContext(context.Background(), userRLSSettings(id), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(context.Background(), `UPDATE users SET name = $1 WHERE id = $2`, name, id)
		return err
	})
	if err != nil {
		log.Println("Failed to update user name:", err)
	}
	return err
}

// BumpUserAvatarVersion advances the avatar version without storing bytes in
// Postgres; the PNG itself lives in the object store (R2). Used after a
// successful avatar upload so clients cache-bust the new image.
func (db *Database) BumpUserAvatarVersion(id string) (int64, error) {
	var version int64
	err := db.TestingWithRLSContext(context.Background(), userRLSSettings(id), func(tx *sql.Tx) error {
		return tx.QueryRowContext(
			context.Background(),
			`UPDATE users SET avatar_version = avatar_version + 1, avatar_updated_at = NOW() WHERE id = $1 RETURNING avatar_version`,
			id,
		).Scan(&version)
	})
	if err != nil {
		log.Println("Failed to bump user avatar version:", err)
	}
	return version, err
}

// GetUserAvatarVersion returns the current avatar version (0 when the user has
// never set an avatar), used to build the ETag and decide whether to serve.
func (db *Database) GetUserAvatarVersion(id string) (int64, error) {
	var version int64
	err := db.TestingWithRLSContext(context.Background(), userRLSSettings(id), func(tx *sql.Tx) error {
		return tx.QueryRowContext(
			context.Background(),
			`SELECT avatar_version FROM users WHERE id = $1`,
			id,
		).Scan(&version)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		log.Println("Failed to get user avatar version:", err)
		return 0, err
	}
	return version, nil
}

func (db *Database) GetUserByID(id string) (*User, error) {
	var u User
	err := db.TestingWithRLSContext(context.Background(), userRLSSettings(id), func(tx *sql.Tx) error {
		return tx.QueryRowContext(
			context.Background(),
			`SELECT id, license_id, name, username, email, avatar_version,
			        email_updates_enabled, created_at
			 FROM users WHERE id=$1 AND lifecycle_state='active'`,
			id,
		).Scan(&u.ID, &u.LicenseID, &u.Name, &u.Username, &u.Email, &u.AvatarVersion, &u.EmailUpdatesEnabled, &u.CreatedAt)
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		log.Println("Failed to get user by ID:", err)
		return nil, err
	}
	return &u, nil
}

func (db *Database) GetUserSettingsByID(id string) (*UserSettings, error) {
	var settings UserSettings
	err := db.TestingWithRLSContext(context.Background(), userRLSSettings(id), func(tx *sql.Tx) error {
		return tx.QueryRowContext(
			context.Background(),
			`SELECT email_updates_enabled, analytics_enabled, error_reporting_enabled FROM users WHERE id = $1`,
			id,
		).Scan(&settings.EmailUpdatesEnabled, &settings.AnalyticsEnabled, &settings.ErrorReportingEnabled)
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		log.Println("Failed to get user settings:", err)
		return nil, err
	}
	return &settings, nil
}

func (db *Database) UpdateUserSettings(id string, settings UserSettings) error {
	err := db.TestingWithRLSContext(context.Background(), userRLSSettings(id), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(
			context.Background(),
			`UPDATE users SET email_updates_enabled = $1, analytics_enabled = $2, error_reporting_enabled = $3 WHERE id = $4`,
			settings.EmailUpdatesEnabled,
			settings.AnalyticsEnabled,
			settings.ErrorReportingEnabled,
			id,
		)
		return err
	})
	if err != nil {
		log.Println("Failed to update user settings:", err)
	}
	return err
}

func (db *Database) UpdateTelemetryPreferences(id string, analyticsEnabled, errorReportingEnabled bool) error {
	err := db.TestingWithRLSContext(context.Background(), userRLSSettings(id), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(context.Background(), `UPDATE users SET analytics_enabled = $1, error_reporting_enabled = $2 WHERE id = $3`, analyticsEnabled, errorReportingEnabled, id)
		return err
	})
	if err != nil {
		log.Println("Failed to update telemetry preferences:", err)
	}
	return err
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func TestingNormalizeUsername(username string) (string, error) {
	username = strings.ToLower(strings.TrimSpace(username))
	if len(username) < 3 || len(username) > 30 {
		return "", ErrInvalidUsername
	}
	for _, character := range username {
		if !((character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '_') {
			return "", ErrInvalidUsername
		}
	}
	return username, nil
}

func defaultUsernameForEmail(email string) string {
	localPart, _, _ := strings.Cut(normalizeEmail(email), "@")
	var username strings.Builder
	lastWasUnderscore := false
	for _, character := range localPart {
		allowed := (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '_'
		if allowed {
			username.WriteRune(character)
			lastWasUnderscore = character == '_'
		} else if username.Len() > 0 && !lastWasUnderscore {
			username.WriteByte('_')
			lastWasUnderscore = true
		}
		if username.Len() == 30 {
			break
		}
	}
	result := strings.Trim(username.String(), "_")
	if len(result) < 3 {
		return "user_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	}
	return result
}
