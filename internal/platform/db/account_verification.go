package db

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

var ErrEmailVerificationInvalid = errors.New("email verification link is invalid or expired")

// EmailVerifiedAt reports when an account confirmed its email, if it has.
func (db *Database) EmailVerifiedAt(ctx context.Context, userID string) (*time.Time, error) {
	var verified sql.NullTime
	err := db.WithRLSContext(ctx, ServiceRLSSettings(), func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT email_verified_at FROM users WHERE id=$1`, userID).Scan(&verified)
	})
	if err != nil || !verified.Valid {
		return nil, err
	}
	return &verified.Time, nil
}

// MarkEmailVerified records that an identity provider already verified the email.
func (db *Database) MarkEmailVerified(ctx context.Context, userID string) error {
	return db.WithRLSContext(ctx, ServiceRLSSettings(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE users SET email_verified_at=COALESCE(email_verified_at,now()) WHERE id=$1`, userID)
		return err
	})
}

func (db *Database) UpsertEmailVerificationToken(ctx context.Context, userID, hashedToken string, expiresAt time.Time) error {
	return db.WithRLSContext(ctx, ServiceRLSSettings(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO email_verification_tokens(user_id,hashed_token,expires_at) VALUES($1,$2,$3)
 ON CONFLICT(user_id) DO UPDATE SET hashed_token=EXCLUDED.hashed_token,expires_at=EXCLUDED.expires_at,created_at=now()`, userID, hashedToken, expiresAt)
		return err
	})
}

// EmailVerificationSentAt is when the latest link was sent, for resend pacing.
func (db *Database) EmailVerificationSentAt(ctx context.Context, userID string) (*time.Time, error) {
	var sent sql.NullTime
	err := db.WithRLSContext(ctx, ServiceRLSSettings(), func(tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx, `SELECT created_at FROM email_verification_tokens WHERE user_id=$1`, userID).Scan(&sent)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	})
	if err != nil || !sent.Valid {
		return nil, err
	}
	return &sent.Time, nil
}

// VerifyEmailWithToken consumes a verification link and returns its account.
func (db *Database) VerifyEmailWithToken(ctx context.Context, hashedToken string, now time.Time) (string, error) {
	var userID string
	err := db.WithRLSContext(ctx, ServiceRLSSettings(), func(tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx, `DELETE FROM email_verification_tokens WHERE hashed_token=$1 AND expires_at>$2 RETURNING user_id`, hashedToken, now).Scan(&userID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrEmailVerificationInvalid
		}
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE users SET email_verified_at=COALESCE(email_verified_at,$2) WHERE id=$1`, userID, now)
		return err
	})
	return userID, err
}
