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

	"github.com/misty-org/misty-server/internal/avatars"
)

type User struct {
	ID                  string
	LicenseID           string
	Username            string
	Email               string
	Provider            string
	ProviderSubject     string
	AvatarVersion       int64
	AvatarDecoration    avatars.Decoration
	BannerVersion       int64
	EmailUpdatesEnabled bool
	CreatedAt           time.Time
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
	hash, err := bcrypt.GenerateFromPassword([]byte(password), PasswordCost)
	if err != nil {
		return nil, err
	}
	return db.createIdentity(username, email, string(hash), "misty", "", false)
}

func (db Store) UpgradePasswordHash(id, hash, password string) {
	cost, err := bcrypt.Cost([]byte(hash))
	if err != nil || cost >= PasswordCost {
		return
	}
	upgraded, err := bcrypt.GenerateFromPassword([]byte(password), PasswordCost)
	if err != nil {
		log.Println("Failed to upgrade password hash:", err)
		return
	}
	err = db.Transaction(context.Background(), UserScope(id), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(context.Background(),
			`UPDATE users SET password_hash=$1 WHERE id=$2 AND password_hash=$3 AND provider='misty'`, string(upgraded), id, hash)
		return err
	})
	if err != nil {
		log.Println("Failed to upgrade password hash:", err)
	}
}

func (db Store) CreateConfirmedUser(username, email, hash string) (*User, error) {
	for attempt := 0; attempt < 3; attempt++ {
		user, err := db.createIdentity(username, email, hash, "misty", "", true)
		if !errors.Is(err, ErrUsernameTaken) {
			return user, err
		}
		username = usernameWithSuffix(username)
	}
	return nil, ErrUsernameTaken
}

func (db Store) createIdentity(username, email, hash, provider, subject string, verified bool) (*User, error) {
	normalizedEmail := NormalizeEmail(email)

	id := uuid.New().String()
	now := time.Now()
	licenseID := uuid.New().String()

	err := db.Transaction(context.Background(), RegistrationScope(id, licenseID, normalizedEmail), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(
			context.Background(),
			`INSERT INTO users (id, license_id, username, email, password_hash, provider, provider_subject, email_verified_at)
			 VALUES ($1, $2, $3, $4, $5, $6, NULLIF($7,''), CASE WHEN $8 THEN now() END)`,
			id, licenseID, username, normalizedEmail, hash, provider, subject, verified,
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
			`SELECT id, license_id, username, email, password_hash, created_at, provider, COALESCE(provider_subject,'')
			 FROM users WHERE LOWER(email)=$1 AND lifecycle_state='active'`,
			normalizedEmail,
		).Scan(&u.ID, &u.LicenseID, &u.Username, &u.Email, &hash, &u.CreatedAt, &u.Provider, &u.ProviderSubject)
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

func (db Store) BumpUserAvatarVersion(id, objectKey string) (int64, error) {
	var version int64
	err := db.Transaction(context.Background(), UserScope(id), func(tx *sql.Tx) error {
		return tx.QueryRowContext(
			context.Background(),
			`UPDATE users SET avatar_version = GREATEST(avatar_version + 1, (EXTRACT(EPOCH FROM clock_timestamp())*1000)::bigint),
			        avatar_updated_at = NOW(), avatar_object_key = $2 WHERE id = $1 RETURNING avatar_version`,
			id, objectKey,
		).Scan(&version)
	})
	if err != nil {
		log.Println("Failed to bump user avatar version:", err)
	}
	return version, err
}

func (db Store) RemoveUserAvatar(id string) error {
	return db.Transaction(context.Background(), UserScope(id), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(context.Background(),
			`UPDATE users SET avatar_version = 0, avatar_updated_at = NOW() WHERE id = $1`, id)
		return err
	})
}

func (db Store) SetUserAvatarDecoration(id string, decoration avatars.Decoration) (avatars.Decoration, error) {
	var out avatars.Decoration
	err := db.Transaction(context.Background(), UserScope(id), func(tx *sql.Tx) error {
		return tx.QueryRowContext(context.Background(),
			`UPDATE users SET avatar_decoration = $2 WHERE id = $1 AND lifecycle_state='active' RETURNING avatar_decoration`,
			id, decoration).Scan(&out)
	})
	return out, err
}

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

func (db Store) BumpUserBannerVersion(id, objectKey string) (int64, error) {
	var version int64
	err := db.Transaction(context.Background(), UserScope(id), func(tx *sql.Tx) error {
		return tx.QueryRowContext(
			context.Background(),
			`UPDATE users SET banner_version = GREATEST(banner_version + 1, (EXTRACT(EPOCH FROM clock_timestamp())*1000)::bigint),
			        banner_updated_at = NOW(), banner_object_key = $2 WHERE id = $1 RETURNING banner_version`,
			id, objectKey,
		).Scan(&version)
	})
	if err != nil {
		log.Println("Failed to bump user banner version:", err)
	}
	return version, err
}

func (db Store) RemoveUserBanner(id string) error {
	return db.Transaction(context.Background(), UserScope(id), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(context.Background(),
			`UPDATE users SET banner_version = 0, banner_updated_at = NOW() WHERE id = $1`, id)
		return err
	})
}

type UserBannerReference struct {
	Version   int64
	ObjectKey string
}

func (db Store) GetUserBannerReference(id string) (UserBannerReference, error) {
	var banner UserBannerReference
	err := db.Transaction(context.Background(), UserScope(id), func(tx *sql.Tx) error {
		return tx.QueryRowContext(context.Background(),
			`SELECT banner_version,COALESCE(banner_object_key,'banners/'||id) FROM users WHERE id=$1`, id).Scan(&banner.Version, &banner.ObjectKey)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return banner, nil
	}
	return banner, err
}

func (db Store) GetUserByID(id string) (*User, error) {
	var u User
	err := db.Transaction(context.Background(), UserScope(id), func(tx *sql.Tx) error {
		return tx.QueryRowContext(
			context.Background(),
			`SELECT id, license_id, username, email, avatar_version, avatar_decoration, banner_version,
			        email_updates_enabled, created_at, provider, COALESCE(provider_subject,'')
			 FROM users WHERE id=$1 AND lifecycle_state='active'`,
			id,
		).Scan(&u.ID, &u.LicenseID, &u.Username, &u.Email, &u.AvatarVersion, &u.AvatarDecoration, &u.BannerVersion, &u.EmailUpdatesEnabled, &u.CreatedAt, &u.Provider, &u.ProviderSubject)
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

func (db Store) SetUsername(id, username string) (string, error) {
	normalized, err := NormalizeUsername(username)
	if err != nil {
		return "", err
	}
	err = db.Transaction(context.Background(), UserScope(id), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(context.Background(), `UPDATE users SET username=$2 WHERE id=$1 AND lifecycle_state='active'`, id, normalized)
		return err
	})
	var pqError *pq.Error
	if errors.As(err, &pqError) && pqError.Constraint == "users_username_unique_idx" {
		return "", ErrUsernameTaken
	}
	return normalized, err
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

var (
	ErrWrongCurrentPassword = errors.New("current password is wrong")
	ErrNoPasswordAccount    = errors.New("this account has no Misty password")
)

func (db Store) ChangePassword(id, current, next, keepSessionHash string) error {
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
		if hash != "" && bcrypt.CompareHashAndPassword([]byte(hash), []byte(current)) != nil {
			return ErrWrongCurrentPassword
		}
		updated, err := bcrypt.GenerateFromPassword([]byte(next), PasswordCost)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(context.Background(), `UPDATE users SET password_hash=$1 WHERE id=$2`, string(updated), id); err != nil {
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
