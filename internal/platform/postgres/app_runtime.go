package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

const AppRuntimeSessionTTL = 5 * time.Minute

var (
	ErrAppRuntimeForbidden = errors.New("app runtime access forbidden")
	ErrAppRecordInvalid    = errors.New("app personal record invalid")
)

type AppRuntimeSession struct {
	UserID    string    `json:"-"`
	AppID     string    `json:"app_id"`
	SpaceID   string    `json:"space_id,omitempty"`
	Scopes    []string  `json:"scopes"`
	ExpiresAt time.Time `json:"expires_at"`
}

type AppPersonalRecord struct {
	Key       string          `json:"key"`
	Data      json.RawMessage `json:"data"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

func (db *Database) CreateAppRuntimeSession(
	ctx context.Context,
	userID, appID, tokenHash, spaceID string,
	ttl time.Duration,
) (*AppRuntimeSession, error) {
	userID, appID, tokenHash, spaceID = strings.TrimSpace(userID), strings.TrimSpace(appID), strings.TrimSpace(tokenHash), strings.TrimSpace(spaceID)
	if userID == "" || appID == "" || len(tokenHash) != 64 || ttl <= 0 || ttl > AppRuntimeSessionTTL {
		return nil, ErrAppRuntimeForbidden
	}
	var result AppRuntimeSession
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		if spaceID != "" {
			var member bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(
				SELECT 1 FROM space_members m JOIN spaces s ON s.id=m.space_id
				WHERE m.user_id=$1 AND m.space_id=$2 AND s.lifecycle_state='active'
			)`, userID, spaceID).Scan(&member); err != nil {
				return err
			}
			if !member {
				return ErrAppRuntimeForbidden
			}
		}
		var scopesRaw []byte
		if err := tx.QueryRowContext(ctx, `SELECT granted_scopes
			FROM user_app_installations WHERE user_id=$1 AND app_id=$2 AND state='installed' FOR SHARE`, userID, appID).Scan(&scopesRaw); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrAppNotInstalled
			}
			return err
		}
		if err := json.Unmarshal(scopesRaw, &result.Scopes); err != nil {
			return err
		}
		result.UserID, result.AppID, result.SpaceID = userID, appID, spaceID
		result.ExpiresAt = time.Now().UTC().Add(ttl)
		_, err := tx.ExecContext(ctx, `INSERT INTO app_runtime_sessions(token_hash,user_id,app_id,space_id,scopes,expires_at)
			VALUES($1,$2,$3,NULLIF($4,''),$5::jsonb,$6)`, tokenHash, userID, appID, spaceID, scopesRaw, result.ExpiresAt)
		return err
	})
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func (db *Database) AppRuntimeSessionByToken(ctx context.Context, tokenHash string) (*AppRuntimeSession, error) {
	tokenHash = strings.TrimSpace(tokenHash)
	if len(tokenHash) != 64 {
		return nil, nil
	}
	var result AppRuntimeSession
	var scopesRaw []byte
	err := db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT s.user_id,s.app_id,COALESCE(s.space_id,''),s.scopes,s.expires_at
			FROM app_runtime_sessions s JOIN user_app_installations i ON i.user_id=s.user_id AND i.app_id=s.app_id
			WHERE s.token_hash=$1 AND s.expires_at>NOW() AND i.state='installed'
			AND s.scopes <@ i.granted_scopes`, tokenHash).
			Scan(&result.UserID, &result.AppID, &result.SpaceID, &scopesRaw, &result.ExpiresAt)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(scopesRaw, &result.Scopes); err != nil {
		return nil, err
	}
	return &result, nil
}

func (db *Database) PutAppPersonalRecord(ctx context.Context, session AppRuntimeSession, key string, data json.RawMessage) (*AppPersonalRecord, error) {
	key = strings.TrimSpace(key)
	if key == "" || len(key) > 160 || !json.Valid(data) {
		return nil, ErrAppRecordInvalid
	}
	var result AppPersonalRecord
	err := db.TestingWithRLSContext(ctx, userRLSSettings(session.UserID), func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `INSERT INTO app_personal_records(user_id,app_id,record_key,data)
			VALUES($1,$2,$3,$4::jsonb) ON CONFLICT(user_id,app_id,record_key) DO UPDATE SET data=EXCLUDED.data,updated_at=NOW()
			RETURNING record_key,data,created_at,updated_at`, session.UserID, session.AppID, key, data).
			Scan(&result.Key, &result.Data, &result.CreatedAt, &result.UpdatedAt)
	})
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func (db *Database) AppPersonalRecords(ctx context.Context, session AppRuntimeSession) ([]AppPersonalRecord, error) {
	items := []AppPersonalRecord{}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(session.UserID), func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT record_key,data,created_at,updated_at FROM app_personal_records
			WHERE user_id=$1 AND app_id=$2 ORDER BY record_key`, session.UserID, session.AppID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item AppPersonalRecord
			if err := rows.Scan(&item.Key, &item.Data, &item.CreatedAt, &item.UpdatedAt); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}

func (db *Database) DeleteAppPersonalRecord(ctx context.Context, session AppRuntimeSession, key string) (bool, error) {
	key = strings.TrimSpace(key)
	if key == "" || len(key) > 160 {
		return false, ErrAppRecordInvalid
	}
	var deleted bool
	err := db.TestingWithRLSContext(ctx, userRLSSettings(session.UserID), func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `DELETE FROM app_personal_records WHERE user_id=$1 AND app_id=$2 AND record_key=$3`, session.UserID, session.AppID, key)
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		deleted = count > 0
		return err
	})
	return deleted, err
}

func (db *Database) PurgeExpiredAppRuntimeSessions(ctx context.Context, limit int) (int, error) {
	if limit < 1 || limit > 5000 {
		limit = 500
	}
	count := 0
	err := db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `DELETE FROM app_runtime_sessions WHERE token_hash IN (
			SELECT token_hash FROM app_runtime_sessions WHERE expires_at<=NOW() ORDER BY expires_at LIMIT $1
		)`, limit)
		if err != nil {
			return err
		}
		removed, err := result.RowsAffected()
		count = int(removed)
		return err
	})
	return count, err
}
