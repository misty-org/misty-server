package db

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"time"
)

var ErrEmailVerificationInvalid = errors.New("email verification link is invalid or expired")
var ErrReferralCodeUnknown = errors.New("referral code not found")
var ErrReferralNotClaimable = errors.New("this account can no longer claim a referral")

// EmailVerifiedAt reports when an account confirmed its email, if it has.
func (db *Database) EmailVerifiedAt(ctx context.Context, userID string) (*time.Time, error) {
	var verified sql.NullTime
	err := db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT email_verified_at FROM users WHERE id=$1`, userID).Scan(&verified)
	})
	if err != nil || !verified.Valid {
		return nil, err
	}
	return &verified.Time, nil
}

// MarkEmailVerified records that an identity provider already verified the email.
func (db *Database) MarkEmailVerified(ctx context.Context, userID string) error {
	return db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE users SET email_verified_at=COALESCE(email_verified_at,now()) WHERE id=$1`, userID)
		return err
	})
}

func (db *Database) UpsertEmailVerificationToken(ctx context.Context, userID, hashedToken string, expiresAt time.Time) error {
	return db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO email_verification_tokens(user_id,hashed_token,expires_at) VALUES($1,$2,$3)
 ON CONFLICT(user_id) DO UPDATE SET hashed_token=EXCLUDED.hashed_token,expires_at=EXCLUDED.expires_at,created_at=now()`, userID, hashedToken, expiresAt)
		return err
	})
}

// EmailVerificationSentAt is when the latest link was sent, for resend pacing.
func (db *Database) EmailVerificationSentAt(ctx context.Context, userID string) (*time.Time, error) {
	var sent sql.NullTime
	err := db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), func(tx *sql.Tx) error {
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
	err := db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), func(tx *sql.Tx) error {
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

// RecordSignupSignals keeps hashed signup network and device for referral checks.
func (db *Database) RecordSignupSignals(ctx context.Context, userID, networkHash, deviceHash string) error {
	return db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO account_signup_signals(user_id,network_hash,device_hash) VALUES($1,$2,$3) ON CONFLICT(user_id) DO NOTHING`, userID, networkHash, deviceHash)
		return err
	})
}

const referralAlphabet = "abcdefghjkmnpqrstuvwxyz23456789"

// ReferralCode returns the account's code, creating it on first use.
func (db *Database) ReferralCode(ctx context.Context, userID string) (string, error) {
	var code string
	err := db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx, `SELECT code FROM referral_codes WHERE user_id=$1`, userID).Scan(&code)
		if err == nil || !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		for range 5 {
			buf := make([]byte, 8)
			if _, err = rand.Read(buf); err != nil {
				return err
			}
			for i := range buf {
				buf[i] = referralAlphabet[int(buf[i])%len(referralAlphabet)]
			}
			candidate := string(buf)
			result, err := tx.ExecContext(ctx, `INSERT INTO referral_codes(user_id,code) VALUES($1,$2) ON CONFLICT DO NOTHING`, userID, candidate)
			if err != nil {
				return err
			}
			if n, _ := result.RowsAffected(); n == 1 {
				code = candidate
				return nil
			}
		}
		return errors.New("could not allocate a referral code")
	})
	return code, err
}

// ReferralClaim is a new account's claim of the link it joined through.
type ReferralClaim struct {
	ReferrerID  string
	SameNetwork bool
	SameDevice  bool
	Sent        bool
}

// ClaimReferral attributes a new account (at most a week old, with no earlier
// claim) to the code's owner, comparing hashed signup signals with the owner's.
func (db *Database) ClaimReferral(ctx context.Context, userID, code, networkHash, deviceHash string, now time.Time) (*ReferralClaim, error) {
	claim := &ReferralClaim{}
	err := db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx, `SELECT user_id FROM referral_codes WHERE code=$1`, code).Scan(&claim.ReferrerID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrReferralCodeUnknown
		}
		if err != nil {
			return err
		}
		if claim.ReferrerID == userID {
			return ErrReferralNotClaimable
		}
		var created time.Time
		if err = tx.QueryRowContext(ctx, `SELECT created_at FROM users WHERE id=$1`, userID).Scan(&created); err != nil {
			return err
		}
		if now.Sub(created) > 7*24*time.Hour {
			return ErrReferralNotClaimable
		}
		var network, device string
		err = tx.QueryRowContext(ctx, `SELECT network_hash,device_hash FROM account_signup_signals WHERE user_id=$1`, claim.ReferrerID).Scan(&network, &device)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		claim.SameNetwork = network != "" && network == networkHash
		claim.SameDevice = device != "" && device == deviceHash
		result, err := tx.ExecContext(ctx, `INSERT INTO referral_claims(user_id,referrer_id,same_network,same_device) VALUES($1,$2,$3,$4) ON CONFLICT(user_id) DO NOTHING`, userID, claim.ReferrerID, claim.SameNetwork, claim.SameDevice)
		if err != nil {
			return err
		}
		if n, _ := result.RowsAffected(); n == 0 {
			return ErrReferralNotClaimable
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return claim, nil
}

// PendingReferralClaim is an unsent claim, if the account has one.
func (db *Database) PendingReferralClaim(ctx context.Context, userID string) (*ReferralClaim, error) {
	claim := &ReferralClaim{}
	var sent sql.NullTime
	err := db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT referrer_id,same_network,same_device,sent_at FROM referral_claims WHERE user_id=$1`, userID).Scan(&claim.ReferrerID, &claim.SameNetwork, &claim.SameDevice, &sent)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	claim.Sent = sent.Valid
	return claim, nil
}

func (db *Database) MarkReferralClaimSent(ctx context.Context, userID string) error {
	return db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE referral_claims SET sent_at=now() WHERE user_id=$1 AND sent_at IS NULL`, userID)
		return err
	})
}
