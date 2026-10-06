package db

import (
	"context"
	"database/sql"
	"time"
)

type CloudConnection struct {
	ID, UserID, Provider, Name, AccountID, AccountDisplay string
	ConnectedAccountID, Status, LastErrorCode             string
	CredentialCiphertext, CredentialNonce                 []byte
	KeyVersion                                            int16
	UsesCustomOAuthClient                                 bool
	ExpiresAt                                             *time.Time
	CreatedAt, UpdatedAt                                  time.Time
}

func (db *Database) CloudConnection(ctx context.Context, userID, id string) (*CloudConnection, error) {
	out := &CloudConnection{}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT c.id,c.user_id,c.provider,c.name,c.account_id,c.account_display,
			c.credential_ciphertext,c.credential_nonce,c.key_version,c.uses_custom_oauth_client,COALESCE(a.expires_at,c.expires_at),
			COALESCE(c.connected_account_id,''),
			CASE WHEN a.status IS NOT NULL AND a.status<>'active' THEN a.status ELSE c.status END,
			CASE WHEN a.status IS NOT NULL AND a.status<>'active' THEN a.last_error_code ELSE c.last_error_code END,
			c.created_at,c.updated_at FROM cloud_connections c
			LEFT JOIN connected_accounts a ON a.id=c.connected_account_id
			WHERE c.id=$1 AND c.user_id=$2 AND c.revoked_at IS NULL`, id, userID).
			Scan(&out.ID, &out.UserID, &out.Provider, &out.Name, &out.AccountID, &out.AccountDisplay,
				&out.CredentialCiphertext, &out.CredentialNonce, &out.KeyVersion,
				&out.UsesCustomOAuthClient, &out.ExpiresAt, &out.ConnectedAccountID, &out.Status,
				&out.LastErrorCode, &out.CreatedAt, &out.UpdatedAt)
	})
	if err == sql.ErrNoRows {
		return nil, ErrSpaceNotFound
	}
	return out, err
}
