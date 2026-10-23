package accounts

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"golang.org/x/crypto/bcrypt"
)

type User struct {
	ID                  string
	LicenseID           string
	Username            string
	Email               string
	Provider            string
	ProviderSubject     string
	AvatarVersion       int64
	EmailUpdatesEnabled bool
	CreatedAt           time.Time
	// CredentialPassword: the password hash is of a value the app derived on the
	// device, so the server has never seen the password itself.
	CredentialPassword bool
}

type UserSettings struct {
	EmailUpdatesEnabled   bool `json:"email_updates_enabled"`
	AnalyticsEnabled      bool `json:"analytics_enabled"`
	ErrorReportingEnabled bool `json:"error_reporting_enabled"`
}

func (db Store) CreateUser(email, password string) (*User, error) {
	return db.createUser(DefaultUsernameForEmail(email), email, password)
}

func (db Store) CreateUserWithUsername(username, email, password string) (*User, error) {
	normalizedUsername, err := NormalizeUsername(username)
	if err != nil {
		return nil, err
	}
	return db.createUser(normalizedUsername, email, password)
}

func (db Store) createUser(username, email, password string) (*User, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	return db.createIdentity(username, email, string(hash), "misty", "")
}

func (db Store) createIdentity(username, email, hash, provider, subject string) (*User, error) {
	normalizedEmail := NormalizeEmail(email)

	id := uuid.New().String()
	now := time.Now()
	licenseID := uuid.New().String()

	err := db.Transaction(context.Background(), RegistrationScope(id, licenseID, normalizedEmail), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(
			context.Background(),
			`INSERT INTO users (id, license_id, username, email, password_hash, provider, provider_subject) VALUES ($1, $2, $3, $4, $5, $6, NULLIF($7,''))`,
			id, licenseID, username, normalizedEmail, hash, provider, subject,
		)
		if err != nil {
			return err
		}

		err = db.CreateIdentity(tx, licenseID, id)
		return err
	})
	if err != nil {
		var pqError *pq.Error
		if errors.As(err, &pqError) && pqError.Constraint == "users_username_unique_idx" {
			return nil, ErrUsernameTaken
		}
		if errors.As(err, &pqError) && (pqError.Constraint == "users_email_key" || pqError.Constraint == "users_email_normalized_unique_idx") {
			return nil, ErrEmailTaken
		}
		log.Println("Failed to create user:", err)
		return nil, err
	}

	return &User{ID: id, LicenseID: licenseID, Username: username, Email: normalizedEmail, Provider: provider, ProviderSubject: subject, CreatedAt: now}, nil
}

func (db Store) GetUserByEmail(email string) (*User, string, error) {
	var u User
	var hash string
	normalizedEmail := NormalizeEmail(email)

	err := db.Transaction(context.Background(), AnonymousScope(normalizedEmail), func(tx *sql.Tx) error {
		return tx.QueryRowContext(
			context.Background(),
			`SELECT id, license_id, username, email, password_hash, created_at, provider, COALESCE(provider_subject,''), credential_password
			 FROM users WHERE LOWER(email)=$1 AND lifecycle_state='active'`,
			normalizedEmail,
		).Scan(&u.ID, &u.LicenseID, &u.Username, &u.Email, &hash, &u.CreatedAt, &u.Provider, &u.ProviderSubject, &u.CredentialPassword)
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

// BumpUserAvatarVersion advances the avatar version without storing bytes in
// Postgres; the PNG itself lives in the object store (R2). Used after a
// successful avatar upload so clients cache-bust the new image.
func (db Store) BumpUserAvatarVersion(id string) (int64, error) {
	var version int64
	err := db.Transaction(context.Background(), UserScope(id), func(tx *sql.Tx) error {
		return tx.QueryRowContext(
			context.Background(),
			`UPDATE users SET avatar_version = avatar_version + 1, avatar_updated_at = NOW(), avatar_object_key = NULL WHERE id = $1 RETURNING avatar_version`,
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
func (db Store) GetUserAvatarVersion(id string) (int64, error) {
	avatar, err := db.GetUserAvatarReference(id)
	return avatar.Version, err
}

type UserAvatarReference struct {
	Version   int64
	ObjectKey string
}

func (db Store) GetUserAvatarReference(id string) (UserAvatarReference, error) {
	var avatar UserAvatarReference
	err := db.Transaction(context.Background(), UserScope(id), func(tx *sql.Tx) error {
		return tx.QueryRowContext(context.Background(),
			`SELECT avatar_version,COALESCE(avatar_object_key,'avatars/'||id) FROM users WHERE id=$1`, id).Scan(&avatar.Version, &avatar.ObjectKey)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return avatar, nil
	}
	return avatar, err
}

func (db Store) GetUserByID(id string) (*User, error) {
	var u User
	err := db.Transaction(context.Background(), UserScope(id), func(tx *sql.Tx) error {
		return tx.QueryRowContext(
			context.Background(),
			`SELECT id, license_id, username, email, avatar_version,
			        email_updates_enabled, created_at, provider, COALESCE(provider_subject,''), credential_password
			 FROM users WHERE id=$1 AND lifecycle_state='active'`,
			id,
		).Scan(&u.ID, &u.LicenseID, &u.Username, &u.Email, &u.AvatarVersion, &u.EmailUpdatesEnabled, &u.CreatedAt, &u.Provider, &u.ProviderSubject, &u.CredentialPassword)
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

func (db Store) GetUserSettingsByID(id string) (*UserSettings, error) {
	var settings UserSettings
	err := db.Transaction(context.Background(), UserScope(id), func(tx *sql.Tx) error {
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

func (db Store) UpdateUserSettings(id string, settings UserSettings) error {
	err := db.Transaction(context.Background(), UserScope(id), func(tx *sql.Tx) error {
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

func (db Store) UpdateTelemetryPreferences(id string, analyticsEnabled, errorReportingEnabled bool) error {
	err := db.Transaction(context.Background(), UserScope(id), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(context.Background(), `UPDATE users SET analytics_enabled = $1, error_reporting_enabled = $2 WHERE id = $3`, analyticsEnabled, errorReportingEnabled, id)
		return err
	})
	if err != nil {
		log.Println("Failed to update telemetry preferences:", err)
	}
	return err
}

var (
	// ErrWrongCurrentPassword means the current password did not match.
	ErrWrongCurrentPassword = errors.New("current password is wrong")
	// ErrNoPasswordAccount means the account signs in another way (Google).
	ErrNoPasswordAccount = errors.New("this account has no Misty password")
)

// ChangePassword replaces a password account's password. `current` and `next`
// are opaque to the server: the desktop app sends a value it derived from the
// person's password (see the native sync crate), older accounts send the
// password itself, and bcrypt hashes either. It never returns the hash. Every
// other session of the account ends with the change (the one named by
// keepSessionHash stays), so a stolen session does not outlive the password.
func (db Store) ChangePassword(id, current, next, keepSessionHash string, credential bool) error {
	err := db.Transaction(context.Background(), UserScope(id), func(tx *sql.Tx) error {
		var hash, provider string
		err := tx.QueryRowContext(context.Background(),
			`SELECT password_hash, provider FROM users WHERE id=$1 AND lifecycle_state='active' FOR UPDATE`, id).Scan(&hash, &provider)
		if err != nil {
			return err
		}
		if provider != "misty" {
			return ErrNoPasswordAccount
		}
		if bcrypt.CompareHashAndPassword([]byte(hash), []byte(current)) != nil {
			return ErrWrongCurrentPassword
		}
		updated, err := bcrypt.GenerateFromPassword([]byte(next), bcrypt.DefaultCost)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(context.Background(), `UPDATE users SET password_hash=$1, credential_password=$3 WHERE id=$2`, string(updated), id, credential); err != nil {
			return err
		}
		_, err = tx.ExecContext(context.Background(), `DELETE FROM sessions WHERE user_id=$1 AND token_hash<>$2`, id, keepSessionHash)
		return err
	})
	if err != nil && !errors.Is(err, ErrWrongCurrentPassword) && !errors.Is(err, ErrNoPasswordAccount) {
		log.Println("Failed to change password:", err)
	}
	return err
}

// SetCredentialPassword records whether the account's password hash is of a
// derived credential (see User.CredentialPassword).
func (db Store) SetCredentialPassword(id string, credential bool) error {
	return db.Transaction(context.Background(), UserScope(id), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(context.Background(), `UPDATE users SET credential_password=$1 WHERE id=$2`, credential, id)
		return err
	})
}
