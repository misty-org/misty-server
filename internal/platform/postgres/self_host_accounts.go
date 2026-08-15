package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"golang.org/x/crypto/bcrypt"
)

var (
	ErrSelfHostBootstrapInvalid = errors.New("self-host bootstrap token is invalid or expired")
	ErrSelfHostInviteInvalid    = errors.New("self-host enrollment invitation is invalid or expired")
	ErrSelfHostSubjectBound     = errors.New("self-host entitlement is already bound to another account")
	ErrSelfHostNotAdmin         = errors.New("self-host administrator access required")
)

type SelfHostAccountAccess struct {
	EntitlementSubject   string
	EntitlementExpiresAt time.Time
	IsAdmin              bool
	Disabled             bool
}

func (db *Database) CreateSelfHostBootstrapToken(ctx context.Context, tokenHash string, expiresAt time.Time) error {
	return db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `INSERT INTO self_host_bootstrap_tokens (token_hash,expires_at) SELECT $1,$2 WHERE NOT EXISTS (SELECT 1 FROM self_host_accounts)`, tokenHash, expiresAt)
		if err != nil {
			return err
		}
		rows, err := result.RowsAffected()
		if err == nil && rows != 1 {
			return ErrSelfHostBootstrapInvalid
		}
		return err
	})
}

func (db *Database) CreateSelfHostBootstrapAdmin(ctx context.Context, name, username, email, password, tokenHash, subject string, entitlementExpiresAt time.Time) (*User, error) {
	return db.createSelfHostUser(ctx, name, username, email, password, subject, entitlementExpiresAt, true, func(tx *sql.Tx, _ string) error {
		var hash string
		err := tx.QueryRowContext(ctx, `
			SELECT token_hash FROM self_host_bootstrap_tokens
			WHERE token_hash=$1 AND consumed_at IS NULL AND expires_at > NOW()
			  AND NOT EXISTS (SELECT 1 FROM self_host_accounts)
			FOR UPDATE
		`, tokenHash).Scan(&hash)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrSelfHostBootstrapInvalid
		}
		if err != nil {
			return err
		}
		return err
	}, func(tx *sql.Tx, _ string) error {
		_, err := tx.ExecContext(ctx, `UPDATE self_host_bootstrap_tokens SET consumed_at=NOW() WHERE token_hash=$1`, tokenHash)
		return err
	})
}

func (db *Database) CreateSelfHostEnrolledUser(ctx context.Context, name, username, email, password, invitationHash, subject string, entitlementExpiresAt time.Time) (*User, error) {
	var invitationID string
	return db.createSelfHostUser(ctx, name, username, email, password, subject, entitlementExpiresAt, false, func(tx *sql.Tx, _ string) error {
		err := tx.QueryRowContext(ctx, `
			SELECT id FROM self_host_enrollment_invitations
			WHERE token_hash=$1 AND consumed_at IS NULL AND revoked_at IS NULL AND expires_at > NOW()
			FOR UPDATE
		`, invitationHash).Scan(&invitationID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrSelfHostInviteInvalid
		}
		if err != nil {
			return err
		}
		return err
	}, func(tx *sql.Tx, userID string) error {
		_, err := tx.ExecContext(ctx, `UPDATE self_host_enrollment_invitations SET consumed_by=$2,consumed_at=NOW() WHERE id=$1`, invitationID, userID)
		return err
	})
}

func (db *Database) createSelfHostUser(ctx context.Context, name, username, email, password, subject string, entitlementExpiresAt time.Time, admin bool, authorize, finalize func(*sql.Tx, string) error) (*User, error) {
	normalizedUsername, err := TestingNormalizeUsername(username)
	if err != nil {
		return nil, err
	}
	normalizedEmail := normalizeEmail(email)
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	user := &User{ID: uuid.NewString(), LicenseID: uuid.NewString(), Name: strings.TrimSpace(name), Username: normalizedUsername, Email: normalizedEmail, CreatedAt: time.Now().UTC()}
	err = db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		if err := authorize(tx, user.ID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO users (id,license_id,name,username,email,password_hash) VALUES ($1,$2,$3,$4,$5,$6)`, user.ID, user.LicenseID, user.Name, user.Username, user.Email, passwordHash); err != nil {
			return err
		}
		if _, err := createLicenseTx(tx, user.LicenseID, user.ID, TierBasic, LicenseStatusActive, nil); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO self_host_accounts (user_id,entitlement_subject,entitlement_expires_at,is_admin) VALUES ($1,$2,$3,$4)`, user.ID, subject, entitlementExpiresAt, admin); err != nil {
			return err
		}
		return finalize(tx, user.ID)
	})
	if err != nil {
		var pqError *pq.Error
		if errors.As(err, &pqError) {
			switch pqError.Constraint {
			case "users_username_unique_idx":
				return nil, ErrUsernameTaken
			case "users_email_key":
				return nil, errors.New("email already registered")
			case "self_host_accounts_entitlement_subject_key":
				return nil, ErrSelfHostSubjectBound
			}
		}
		return nil, err
	}
	return user, nil
}

func (db *Database) SelfHostAccountAccess(ctx context.Context, userID string) (SelfHostAccountAccess, error) {
	var access SelfHostAccountAccess
	var disabledAt sql.NullTime
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT entitlement_subject,entitlement_expires_at,is_admin,disabled_at FROM self_host_accounts WHERE user_id=$1`, userID).
			Scan(&access.EntitlementSubject, &access.EntitlementExpiresAt, &access.IsAdmin, &disabledAt)
	})
	if err != nil {
		return access, err
	}
	access.Disabled = disabledAt.Valid
	return access, nil
}

func (db *Database) RenewSelfHostEntitlement(ctx context.Context, userID, subject string, expiresAt time.Time) error {
	return db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE self_host_accounts SET entitlement_expires_at=$3,updated_at=NOW() WHERE user_id=$1 AND entitlement_subject=$2 AND disabled_at IS NULL`, userID, subject, expiresAt)
		if err != nil {
			return err
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if rows != 1 {
			return ErrSelfHostSubjectBound
		}
		return nil
	})
}

func (db *Database) CreateSelfHostInvitation(ctx context.Context, adminUserID, invitationID, tokenHash string, expiresAt time.Time) error {
	return db.TestingWithRLSContext(ctx, userRLSSettings(adminUserID), func(tx *sql.Tx) error {
		var admin bool
		if err := tx.QueryRowContext(ctx, `SELECT is_admin AND disabled_at IS NULL FROM self_host_accounts WHERE user_id=$1`, adminUserID).Scan(&admin); err != nil || !admin {
			return ErrSelfHostNotAdmin
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO self_host_enrollment_invitations (id,token_hash,created_by,expires_at) VALUES ($1,$2,$3,$4)`, invitationID, tokenHash, adminUserID, expiresAt)
		return err
	})
}

func (db *Database) RevokeSelfHostInvitation(ctx context.Context, adminUserID, invitationID string) error {
	return db.TestingWithRLSContext(ctx, userRLSSettings(adminUserID), func(tx *sql.Tx) error {
		var admin bool
		if err := tx.QueryRowContext(ctx, `SELECT is_admin AND disabled_at IS NULL FROM self_host_accounts WHERE user_id=$1`, adminUserID).Scan(&admin); err != nil || !admin {
			return ErrSelfHostNotAdmin
		}
		result, err := tx.ExecContext(ctx, `
			UPDATE self_host_enrollment_invitations SET revoked_at=NOW()
			WHERE id=$1 AND created_by=$2 AND consumed_at IS NULL AND revoked_at IS NULL
		`, invitationID, adminUserID)
		if err != nil {
			return err
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if rows != 1 {
			return ErrSelfHostInviteInvalid
		}
		return nil
	})
}

func (db *Database) ResetSelfHostPassword(ctx context.Context, email, password string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	return db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		var userID string
		err := tx.QueryRowContext(ctx, `UPDATE users SET password_hash=$2 WHERE LOWER(email)=$1 AND EXISTS (SELECT 1 FROM self_host_accounts WHERE user_id=users.id) RETURNING id`, normalizeEmail(email), hash).Scan(&userID)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id=$1`, userID)
		return err
	})
}

func (db *Database) DisableSelfHostAccount(ctx context.Context, email string) error {
	return db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		var userID string
		if err := tx.QueryRowContext(ctx, `SELECT id FROM users WHERE LOWER(email)=$1`, normalizeEmail(email)).Scan(&userID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE self_host_accounts SET disabled_at=NOW(),updated_at=NOW() WHERE user_id=$1`, userID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id=$1`, userID)
		return err
	})
}
