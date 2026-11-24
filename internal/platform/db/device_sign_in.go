package db

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"time"

	"github.com/misty-org/misty-server/internal/accounts"
)

const deviceSignInChannel = "misty_device_sign_in"

const (
	DeviceSignInPending  = "pending"
	DeviceSignInApproved = "approved"
	DeviceSignInRefused  = "refused"
	DeviceSignInExpired  = "expired"
)

var (
	ErrDeviceCodeNotFound    = errors.New("device code not found")
	ErrDeviceSignInExhausted = errors.New("could not choose a device code")
)

type DeviceSignIn struct {
	Name           string
	State          string
	ApprovedUserID string
	CreatedAt      time.Time
	ExpiresAt      time.Time
}

type DeviceSignInOutcome int

const (
	DeviceSignInGone DeviceSignInOutcome = iota
	DeviceSignInDelivered
	DeviceSignInRefusedAtDelivery
)

func (db *Database) CreateDeviceSignIn(ctx context.Context, idHash, name string) (string, time.Time, error) {
	for attempt := 0; attempt < 8; attempt++ {
		code, err := accounts.NewUserCode()
		if err != nil {
			return "", time.Time{}, err
		}
		var expiresAt time.Time
		inserted := false
		err = db.WithRLSContext(ctx, ServiceRLSSettings(), func(tx *sql.Tx) error {
			prefix := accounts.UserCodePrefix(code)
			if _, err := tx.ExecContext(ctx, `UPDATE device_sign_in_requests SET state='expired',decided_at=now()
				WHERE state='pending' AND code_prefix=$1 AND expires_at<=now()`, prefix); err != nil {
				return err
			}
			err := tx.QueryRowContext(ctx, `INSERT INTO device_sign_in_requests(id_hash,name,code_prefix,code_hash,expires_at)
				VALUES($1,$2,$3,$4,now()+make_interval(secs => $5))
				ON CONFLICT (code_prefix) WHERE state='pending' DO NOTHING
				RETURNING expires_at`, idHash, name, prefix, accounts.HashUserCode(code),
				accounts.DeviceSignInTTL.Seconds()).Scan(&expiresAt)
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			inserted = err == nil
			return err
		})
		if err != nil {
			return "", time.Time{}, err
		}
		if inserted {
			return code, expiresAt, nil
		}
	}
	return "", time.Time{}, ErrDeviceSignInExhausted
}

func (db *Database) DeviceSignInByID(ctx context.Context, idHash string) (DeviceSignIn, bool, error) {
	var out DeviceSignIn
	found := false
	err := db.WithRLSContext(ctx, ServiceRLSSettings(), func(tx *sql.Tx) error {
		var approved sql.NullString
		err := tx.QueryRowContext(ctx, `SELECT name,
			CASE WHEN state IN ('pending','approved') AND expires_at<=now() THEN 'expired' ELSE state END,
			approved_user_id,created_at,expires_at
			FROM device_sign_in_requests WHERE id_hash=$1`, idHash).Scan(&out.Name, &out.State, &approved, &out.CreatedAt, &out.ExpiresAt)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		out.ApprovedUserID = approved.String
		found = true
		return nil
	})
	return out, found, err
}

func (db *Database) LookupDeviceSignIn(ctx context.Context, code string) (DeviceSignIn, error) {
	var out DeviceSignIn
	err := db.withDeviceCode(ctx, code, func(tx *sql.Tx, idHash string, request DeviceSignIn) error {
		out = request
		return nil
	})
	return out, err
}

func (db *Database) DecideDeviceSignIn(ctx context.Context, code, userID string, approve bool) error {
	return db.withDeviceCode(ctx, code, func(tx *sql.Tx, idHash string, _ DeviceSignIn) error {
		var err error
		if approve {
			_, err = tx.ExecContext(ctx, `UPDATE device_sign_in_requests SET state='approved',approved_user_id=$2,decided_at=now()
				WHERE id_hash=$1 AND state='pending'`, idHash, userID)
		} else {
			_, err = tx.ExecContext(ctx, `UPDATE device_sign_in_requests SET state='refused',decided_at=now()
				WHERE id_hash=$1 AND state='pending'`, idHash)
		}
		if err != nil {
			return err
		}
		return notifyDeviceSignInTx(ctx, tx, idHash)
	})
}

func (db *Database) withDeviceCode(ctx context.Context, code string, fn func(*sql.Tx, string, DeviceSignIn) error) error {
	wrong := false
	err := db.WithRLSContext(ctx, ServiceRLSSettings(), func(tx *sql.Tx) error {
		var idHash, codeHash string
		var request DeviceSignIn
		var failed int
		err := tx.QueryRowContext(ctx, `SELECT id_hash,code_hash,name,failed_attempts,created_at,expires_at
			FROM device_sign_in_requests WHERE code_prefix=$1 AND state='pending' AND expires_at>now() FOR UPDATE`,
			accounts.UserCodePrefix(code)).Scan(&idHash, &codeHash, &request.Name, &failed, &request.CreatedAt, &request.ExpiresAt)
		if errors.Is(err, sql.ErrNoRows) {
			wrong = true
			return nil
		}
		if err != nil {
			return err
		}
		if subtle.ConstantTimeCompare([]byte(codeHash), []byte(accounts.HashUserCode(code))) != 1 {
			wrong = true
			return recordWrongDeviceCodeTx(ctx, tx, idHash, failed+1)
		}
		request.State = DeviceSignInPending
		return fn(tx, idHash, request)
	})
	if err != nil {
		return err
	}
	if wrong {
		return ErrDeviceCodeNotFound
	}
	return nil
}

func recordWrongDeviceCodeTx(ctx context.Context, tx *sql.Tx, idHash string, failed int) error {
	if failed < accounts.DeviceSignInMaxWrongCodes {
		_, err := tx.ExecContext(ctx, `UPDATE device_sign_in_requests SET failed_attempts=$2 WHERE id_hash=$1`, idHash, failed)
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE device_sign_in_requests SET failed_attempts=$2,state='refused',decided_at=now()
		WHERE id_hash=$1`, idHash, accounts.DeviceSignInMaxWrongCodes); err != nil {
		return err
	}
	return notifyDeviceSignInTx(ctx, tx, idHash)
}

func notifyDeviceSignInTx(ctx context.Context, tx *sql.Tx, idHash string) error {
	_, err := tx.ExecContext(ctx, `SELECT pg_notify('`+deviceSignInChannel+`',$1)`, idHash)
	return err
}

func (db *Database) CompleteDeviceSignIn(ctx context.Context, idHash, userID, sessionHash, refreshHash string, expires time.Time) (DeviceSignInOutcome, error) {
	outcome := DeviceSignInGone
	err := db.WithRLSContext(ctx, ServiceRLSSettings(), func(tx *sql.Tx) error {
		var name, approvedUserID string
		err := tx.QueryRowContext(ctx, `SELECT name,approved_user_id FROM device_sign_in_requests
			WHERE id_hash=$1 AND state='approved' AND expires_at>now() FOR UPDATE`, idHash).Scan(&name, &approvedUserID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if approvedUserID != userID {
			return nil
		}
		var allowed bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=$1 AND lifecycle_state='active')
			AND NOT EXISTS(SELECT 1 FROM account_controls WHERE user_id=$1 AND suspended_at IS NOT NULL)`, userID).Scan(&allowed); err != nil {
			return err
		}
		if !allowed {
			if _, err := tx.ExecContext(ctx, `UPDATE device_sign_in_requests SET state='refused',approved_user_id=NULL,decided_at=now()
				WHERE id_hash=$1`, idHash); err != nil {
				return err
			}
			outcome = DeviceSignInRefusedAtDelivery
			return nil
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM device_sign_in_requests WHERE id_hash=$1`, idHash); err != nil {
			return err
		}
		if err := insertRefreshSessionTx(ctx, tx, sessionHash, refreshHash, userID, name, expires); err != nil {
			return err
		}
		outcome = DeviceSignInDelivered
		return nil
	})
	if err != nil {
		return DeviceSignInGone, err
	}
	return outcome, nil
}

func (db *Database) PurgeExpiredDeviceSignIns(ctx context.Context, limit int) (int, error) {
	if limit < 1 || limit > 5000 {
		limit = 1000
	}
	var purged int64
	err := db.WithRLSContext(ctx, ServiceRLSSettings(), func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `DELETE FROM device_sign_in_requests WHERE ctid IN (
			SELECT ctid FROM device_sign_in_requests WHERE expires_at<=now()-interval '1 minute' LIMIT $1)`, limit)
		if err != nil {
			return err
		}
		purged, err = result.RowsAffected()
		return err
	})
	return int(purged), err
}
