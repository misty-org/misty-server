package db

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

var (
	ErrAccountDeletionBlocked = errors.New("account deletion requires ownership transfer")
	ErrAccountDeletionToken   = errors.New("account deletion status token is invalid")
)

type AccountDeletionBlocker struct {
	SpaceID     string `json:"space_id"`
	Name        string `json:"name"`
	MemberCount int    `json:"member_count"`
}

type AccountDeletionRequest struct {
	ID                       string            `json:"id"`
	UserID                   string            `json:"-"`
	Status                   string            `json:"status"`
	PurgeAfter               time.Time         `json:"purge_after"`
	ProviderRevocationStatus map[string]string `json:"provider_revocation_status"`
	LastErrorCode            string            `json:"last_error_code,omitempty"`
	CreatedAt                time.Time         `json:"created_at"`
	UpdatedAt                time.Time         `json:"updated_at"`
	CompletedAt              *time.Time        `json:"completed_at,omitempty"`
}

func (db *Database) AccountDeletionBlockers(
	ctx context.Context, userID string,
) ([]AccountDeletionBlocker, error) {
	out := []AccountDeletionBlocker{}
	err := db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `
			SELECT s.id,s.name,COUNT(m.user_id)
			FROM spaces s
			LEFT JOIN space_members m ON m.space_id=s.id
			WHERE s.owner_user_id=$1 AND s.lifecycle_state='active' AND s.kind='standard'
			GROUP BY s.id,s.name
			HAVING COUNT(m.user_id)>1
			ORDER BY s.created_at`, userID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item AccountDeletionBlocker
			if err := rows.Scan(&item.SpaceID, &item.Name, &item.MemberCount); err != nil {
				return err
			}
			out = append(out, item)
		}
		return rows.Err()
	})
	return out, err
}

func (db *Database) BeginAccountDeletion(
	ctx context.Context, userID, requestID, statusTokenHash string, retention time.Duration,
) (*AccountDeletionRequest, error) {
	if userID == "" || requestID == "" || statusTokenHash == "" {
		return nil, ErrAccountDeletionToken
	}
	if retention < 24*time.Hour {
		retention = 30 * 24 * time.Hour
	}
	purgeAfter := time.Now().UTC().Add(retention)
	out := &AccountDeletionRequest{}
	err := db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		var state string
		if err := tx.QueryRowContext(
			ctx, `SELECT lifecycle_state FROM users WHERE id=$1 FOR UPDATE`, userID,
		).Scan(&state); err != nil {
			return err
		}
		if state != "active" {
			return ErrAccountDeletionBlocked
		}
		var sharedOwned int
		if err := tx.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM spaces s
			WHERE s.owner_user_id=$1 AND s.lifecycle_state='active' AND s.kind='standard'
			  AND EXISTS(SELECT 1 FROM space_members m WHERE m.space_id=s.id AND m.user_id<>$1)`, userID,
		).Scan(&sharedOwned); err != nil {
			return err
		}
		if sharedOwned > 0 {
			return ErrAccountDeletionBlocked
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO account_deletion_requests(
			    id,user_id,status_token_hash,purge_after
			) VALUES($1,$2,$3,$4)`,
			requestID, userID, statusTokenHash, purgeAfter,
		); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE users SET lifecycle_state='pending_deletion',
			    deletion_requested_at=NOW()
			WHERE id=$1`, userID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id=$1`, userID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE space_agents SET schedules_enabled=FALSE
			WHERE creator_user_id=$1`, userID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE space_workflows SET schedules_enabled=FALSE
			WHERE creator_user_id=$1`, userID); err != nil {
			return err
		}
		if err := disableAccountAgentsTx(ctx, tx, userID); err != nil {
			return err
		}
		return scanAccountDeletionRequest(tx.QueryRowContext(ctx, `
			SELECT id,user_id,status,purge_after,provider_revocation_status,
			       last_error_code,created_at,updated_at,completed_at
			FROM account_deletion_requests WHERE id=$1`, requestID), out)
	})
	return out, err
}

func (db *Database) AccountDeletionStatus(
	ctx context.Context, requestID, statusTokenHash string,
) (*AccountDeletionRequest, error) {
	out := &AccountDeletionRequest{}
	err := db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		return scanAccountDeletionRequest(tx.QueryRowContext(ctx, `
			SELECT id,user_id,status,purge_after,provider_revocation_status,
			       last_error_code,created_at,updated_at,completed_at
			FROM account_deletion_requests
			WHERE id=$1 AND status_token_hash=$2`, requestID, statusTokenHash), out)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrAccountDeletionToken
	}
	return out, err
}

func (db *Database) AccountDeletionConnections(
	ctx context.Context, userID string,
) ([]CloudConnection, error) {
	out := []CloudConnection{}
	err := db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `
			SELECT id,user_id,provider,name,account_id,account_display,
			       credential_ciphertext,credential_nonce,key_version,
			       uses_custom_oauth_client,expires_at,created_at,updated_at
			FROM cloud_connections
			WHERE user_id=$1 AND revoked_at IS NULL`, userID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item CloudConnection
			if err := rows.Scan(
				&item.ID, &item.UserID, &item.Provider, &item.Name,
				&item.AccountID, &item.AccountDisplay,
				&item.CredentialCiphertext, &item.CredentialNonce,
				&item.KeyVersion, &item.UsesCustomOAuthClient,
				&item.ExpiresAt, &item.CreatedAt, &item.UpdatedAt,
			); err != nil {
				return err
			}
			out = append(out, item)
		}
		return rows.Err()
	})
	return out, err
}

func (db *Database) AccountDeletionProviderCredentials(
	ctx context.Context, userID string,
) ([]ProviderCredential, error) {
	out := []ProviderCredential{}
	err := db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `
			SELECT id,integration_id,space_id,user_id,provider,ciphertext,nonce,
			       key_version,account_id,account_display,expires_at
			FROM space_provider_credentials
			WHERE user_id=$1 AND revoked_at IS NULL`, userID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item ProviderCredential
			if err := rows.Scan(
				&item.ID, &item.IntegrationID, &item.SpaceID, &item.UserID,
				&item.Provider, &item.Ciphertext, &item.Nonce,
				&item.KeyVersion, &item.AccountID, &item.AccountDisplay,
				&item.ExpiresAt,
			); err != nil {
				return err
			}
			out = append(out, item)
		}
		return rows.Err()
	})
	return out, err
}

func (db *Database) ProcessingAccountDeletions(
	ctx context.Context, limit int,
) ([]AccountDeletionRequest, error) {
	if limit < 1 || limit > 100 {
		limit = 25
	}
	out := []AccountDeletionRequest{}
	err := db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `
			SELECT id,user_id,status,purge_after,provider_revocation_status,
			       last_error_code,created_at,updated_at,completed_at
			FROM account_deletion_requests
			WHERE status='processing'
			ORDER BY updated_at
			LIMIT $1`, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item AccountDeletionRequest
			if err := scanAccountDeletionRequest(rows, &item); err != nil {
				return err
			}
			out = append(out, item)
		}
		return rows.Err()
	})
	return out, err
}
