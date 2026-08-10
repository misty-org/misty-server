package db

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
)

var ErrCloudConnectionLimit = errors.New("cloud connection limit reached")

type CloudConnection struct {
	ID, UserID, Provider, Name, AccountID, AccountDisplay string
	CredentialCiphertext, CredentialNonce                 []byte
	KeyVersion                                            int16
	UsesCustomOAuthClient                                 bool
	ExpiresAt                                             *time.Time
	CreatedAt, UpdatedAt                                  time.Time
}

type CloudOAuthState struct {
	UserID, Provider, ConnectionName, ReturnTo string
	SecretCiphertext, SecretNonce              []byte
	ExpiresAt                                  time.Time
}

func (db *Database) CloudConnections(ctx context.Context, userID string) ([]CloudConnection, error) {
	var out []CloudConnection
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT id,user_id,provider,name,account_id,account_display,
			uses_custom_oauth_client,expires_at,created_at,updated_at
			FROM cloud_connections WHERE user_id=$1 AND revoked_at IS NULL ORDER BY created_at`, userID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item CloudConnection
			if err := rows.Scan(&item.ID, &item.UserID, &item.Provider, &item.Name, &item.AccountID,
				&item.AccountDisplay, &item.UsesCustomOAuthClient, &item.ExpiresAt,
				&item.CreatedAt, &item.UpdatedAt); err != nil {
				return err
			}
			out = append(out, item)
		}
		return rows.Err()
	})
	return out, err
}

func (db *Database) CloudConnection(ctx context.Context, userID, id string) (*CloudConnection, error) {
	out := &CloudConnection{}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT id,user_id,provider,name,account_id,account_display,
			credential_ciphertext,credential_nonce,key_version,uses_custom_oauth_client,expires_at,
			created_at,updated_at FROM cloud_connections
			WHERE id=$1 AND user_id=$2 AND revoked_at IS NULL`, id, userID).
			Scan(&out.ID, &out.UserID, &out.Provider, &out.Name, &out.AccountID, &out.AccountDisplay,
				&out.CredentialCiphertext, &out.CredentialNonce, &out.KeyVersion,
				&out.UsesCustomOAuthClient, &out.ExpiresAt, &out.CreatedAt, &out.UpdatedAt)
	})
	if err == sql.ErrNoRows {
		return nil, ErrSpaceNotFound
	}
	return out, err
}

func (db *Database) SaveCloudConnection(ctx context.Context, item CloudConnection, maximum int) (*CloudConnection, error) {
	if item.ID == "" {
		item.ID = "cloud_" + uuid.NewString()
	}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(item.UserID), func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, item.UserID); err != nil {
			return err
		}
		if maximum > 0 {
			var connected int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM cloud_connections
				WHERE user_id=$1 AND revoked_at IS NULL AND name<>$2`, item.UserID, item.Name).Scan(&connected); err != nil {
				return err
			}
			if connected >= maximum {
				return ErrCloudConnectionLimit
			}
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM cloud_connections
			WHERE user_id=$1 AND revoked_at IS NOT NULL
			AND (name=$2 OR (provider=$3 AND account_id=$4))`,
			item.UserID, item.Name, item.Provider, item.AccountID); err != nil {
			return err
		}
		return tx.QueryRowContext(ctx, `INSERT INTO cloud_connections
			(id,user_id,provider,name,account_id,account_display,credential_ciphertext,credential_nonce,
			 key_version,uses_custom_oauth_client,expires_at)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
			ON CONFLICT(user_id,name) DO UPDATE SET
			 provider=EXCLUDED.provider,account_id=EXCLUDED.account_id,account_display=EXCLUDED.account_display,
			 credential_ciphertext=EXCLUDED.credential_ciphertext,credential_nonce=EXCLUDED.credential_nonce,
			 key_version=EXCLUDED.key_version,uses_custom_oauth_client=EXCLUDED.uses_custom_oauth_client,
			 expires_at=EXCLUDED.expires_at,revoked_at=NULL,updated_at=NOW()
			RETURNING id,created_at,updated_at`, item.ID, item.UserID, item.Provider, item.Name,
			item.AccountID, item.AccountDisplay, item.CredentialCiphertext, item.CredentialNonce,
			item.KeyVersion, item.UsesCustomOAuthClient, item.ExpiresAt).
			Scan(&item.ID, &item.CreatedAt, &item.UpdatedAt)
	})
	return &item, err
}

func (db *Database) UpdateCloudConnectionCredential(ctx context.Context, item CloudConnection) error {
	return db.TestingWithRLSContext(ctx, userRLSSettings(item.UserID), func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE cloud_connections SET credential_ciphertext=$1,
			credential_nonce=$2,key_version=$3,expires_at=$4,updated_at=NOW()
			WHERE id=$5 AND user_id=$6 AND revoked_at IS NULL`, item.CredentialCiphertext,
			item.CredentialNonce, item.KeyVersion, item.ExpiresAt, item.ID, item.UserID)
		if err != nil {
			return err
		}
		if changed, _ := result.RowsAffected(); changed != 1 {
			return ErrSpaceNotFound
		}
		return nil
	})
}

func (db *Database) DeleteCloudConnection(ctx context.Context, userID, id string) error {
	return db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE cloud_connections SET revoked_at=NOW(),updated_at=NOW()
			WHERE id=$1 AND user_id=$2 AND revoked_at IS NULL`, id, userID)
		if err != nil {
			return err
		}
		if changed, _ := result.RowsAffected(); changed != 1 {
			return ErrSpaceNotFound
		}
		return nil
	})
}

func (db *Database) CreateCloudOAuthState(ctx context.Context, stateHash string, item CloudOAuthState) error {
	return db.TestingWithRLSContext(ctx, userRLSSettings(item.UserID), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO cloud_oauth_states
			(state_hash,user_id,provider,connection_name,secret_ciphertext,secret_nonce,return_to,expires_at)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, stateHash, item.UserID, item.Provider,
			item.ConnectionName, item.SecretCiphertext, item.SecretNonce, item.ReturnTo, item.ExpiresAt)
		return err
	})
}

func (db *Database) ConsumeCloudOAuthState(ctx context.Context, stateHash string) (*CloudOAuthState, error) {
	out := &CloudOAuthState{}
	err := db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `UPDATE cloud_oauth_states SET consumed_at=NOW()
			WHERE state_hash=$1 AND consumed_at IS NULL AND expires_at>NOW()
			RETURNING user_id,provider,connection_name,secret_ciphertext,secret_nonce,return_to,expires_at`,
			stateHash).Scan(&out.UserID, &out.Provider, &out.ConnectionName, &out.SecretCiphertext,
			&out.SecretNonce, &out.ReturnTo, &out.ExpiresAt)
	})
	if err == sql.ErrNoRows {
		return nil, ErrSpaceNotFound
	}
	return out, err
}
