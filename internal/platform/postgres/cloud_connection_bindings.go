package db

import (
	"context"
	"database/sql"
	"time"

	"github.com/google/uuid"
)

type CloudCredentialHandoff struct {
	UserID, CloudConnectionID string
	ExpiresAt                 time.Time
}

func (db *Database) BindConnectedAccountCloudConnection(ctx context.Context, userID string, account ConnectedAccount, provider, name string, maximum int) (*CloudConnection, error) {
	item := &CloudConnection{
		ID: "cloud_" + uuid.NewString(), UserID: userID, Provider: provider, Name: name,
		AccountID: account.AccountID, AccountDisplay: account.AccountDisplay,
		ConnectedAccountID: account.ID, Status: "active", ExpiresAt: account.ExpiresAt,
	}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, userID); err != nil {
			return err
		}
		if maximum > 0 {
			var connected int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM cloud_connections
				WHERE user_id=$1 AND revoked_at IS NULL AND name<>$2`, userID, name).Scan(&connected); err != nil {
				return err
			}
			if connected >= maximum {
				return ErrCloudConnectionLimit
			}
		}
		err := tx.QueryRowContext(ctx, `INSERT INTO cloud_connections
			(id,user_id,provider,name,account_id,account_display,credential_ciphertext,credential_nonce,
			 key_version,uses_custom_oauth_client,expires_at,connected_account_id,status,last_error_code)
			SELECT $1,$2,$3,$4,a.account_id,a.account_display,''::bytea,''::bytea,1,FALSE,
				a.expires_at,a.id,'active','' FROM connected_accounts a
			WHERE a.id=$5 AND a.user_id=$2 AND a.revoked_at IS NULL AND a.status='active'
				AND a.capabilities @> '["files"]'::jsonb
			ON CONFLICT(user_id,name) DO UPDATE SET provider=EXCLUDED.provider,
			 account_id=EXCLUDED.account_id,account_display=EXCLUDED.account_display,
			 connected_account_id=EXCLUDED.connected_account_id,expires_at=EXCLUDED.expires_at,
			 status='active',last_error_code='',revoked_at=NULL,updated_at=NOW()
			RETURNING id,account_id,account_display,expires_at,created_at,updated_at`, item.ID, userID,
			provider, name, item.ConnectedAccountID).
			Scan(&item.ID, &item.AccountID, &item.AccountDisplay, &item.ExpiresAt,
				&item.CreatedAt, &item.UpdatedAt)
		if err == sql.ErrNoRows {
			return ErrSpaceForbidden
		}
		return err
	})
	return item, err
}

func (db *Database) CreateCloudCredentialHandoff(ctx context.Context, handoffHash string, item CloudCredentialHandoff) error {
	return db.TestingWithRLSContext(ctx, userRLSSettings(item.UserID), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO cloud_credential_handoffs
			(handoff_hash,user_id,cloud_connection_id,expires_at) VALUES($1,$2,$3,$4)`,
			handoffHash, item.UserID, item.CloudConnectionID, item.ExpiresAt)
		return err
	})
}

func (db *Database) ConsumeCloudCredentialHandoff(ctx context.Context, handoffHash string) (*CloudCredentialHandoff, error) {
	item := &CloudCredentialHandoff{}
	err := db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `UPDATE cloud_credential_handoffs SET consumed_at=NOW()
			WHERE handoff_hash=$1 AND consumed_at IS NULL AND expires_at>NOW()
			RETURNING user_id,cloud_connection_id,expires_at`, handoffHash).
			Scan(&item.UserID, &item.CloudConnectionID, &item.ExpiresAt)
	})
	if err == sql.ErrNoRows {
		return nil, ErrSpaceNotFound
	}
	return item, err
}
