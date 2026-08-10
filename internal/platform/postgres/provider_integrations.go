package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

func (db *Database) ResolveAgentProviderConnection(ctx context.Context, userID, spaceID, instanceID, provider string) (string, error) {
	var connectionID string
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		var bindings []byte
		if err := tx.QueryRowContext(ctx, `SELECT connection_bindings FROM space_agent_instances WHERE id=$1 AND user_id=$2 AND space_id=$3`, instanceID, userID, spaceID).Scan(&bindings); err != nil {
			return err
		}
		var values map[string]string
		if json.Unmarshal(bindings, &values) != nil || values[provider] == "" {
			return ErrSpaceNotFound
		}
		connectionID = values[provider]
		var valid bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM space_integrations WHERE id=$1 AND connected_by_user_id=$2 AND space_id=$3 AND provider=$4 AND status='active')`, connectionID, userID, spaceID, provider).Scan(&valid); err != nil {
			return err
		}
		if !valid {
			return ErrSpaceNotFound
		}
		return nil
	})
	return connectionID, err
}

type ProviderOAuthState struct {
	UserID             string
	SpaceID            string
	Provider           string
	VerifierCiphertext []byte
	VerifierNonce      []byte
	ReturnTo           string
	ExpiresAt          time.Time
}

type ProviderCredential struct {
	ID             string
	IntegrationID  string
	SpaceID        string
	UserID         string
	Provider       string
	Ciphertext     []byte
	Nonce          []byte
	KeyVersion     int16
	AccountID      string
	AccountDisplay string
	ExpiresAt      *time.Time
}

func (db *Database) CreateProviderOAuthState(ctx context.Context, stateHash string, item ProviderOAuthState) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, item.UserID, item.SpaceID, PermissionIntegrationsManage); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO provider_oauth_states(state_hash,user_id,space_id,provider,verifier_ciphertext,verifier_nonce,return_to,expires_at)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, stateHash, item.UserID, item.SpaceID, item.Provider, item.VerifierCiphertext, item.VerifierNonce, item.ReturnTo, item.ExpiresAt)
		return err
	})
}

// ConsumeProviderOAuthState is atomic and single-use. Expired or replayed
// states deliberately look like missing records.
func (db *Database) ConsumeProviderOAuthState(ctx context.Context, stateHash string) (*ProviderOAuthState, error) {
	out := &ProviderOAuthState{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `UPDATE provider_oauth_states SET consumed_at=NOW()
			WHERE state_hash=$1 AND consumed_at IS NULL AND expires_at>NOW()
			RETURNING user_id,space_id,provider,verifier_ciphertext,verifier_nonce,return_to,expires_at`, stateHash).
			Scan(&out.UserID, &out.SpaceID, &out.Provider, &out.VerifierCiphertext, &out.VerifierNonce, &out.ReturnTo, &out.ExpiresAt)
	})
	if err == sql.ErrNoRows {
		return nil, ErrSpaceNotFound
	}
	return out, err
}

func (db *Database) SaveProviderCredential(ctx context.Context, item ProviderCredential, displayName string, scopes []string) (*SpaceIntegration, error) {
	if item.ID == "" {
		item.ID = "credential_" + uuid.NewString()
	}
	if item.IntegrationID == "" {
		item.IntegrationID = "integration_" + uuid.NewString()
	}
	permissions := mustJSON(scopes)
	out := &SpaceIntegration{ID: item.IntegrationID, SpaceID: item.SpaceID, Provider: item.Provider, DisplayName: displayName, GrantedPermissions: scopes, Status: "active", ConnectedByUserID: item.UserID}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, item.UserID, item.SpaceID, PermissionIntegrationsManage); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO space_integrations(id,space_id,provider,display_name,credential_reference,granted_permissions,status,connected_by_user_id)
			VALUES($1,$2,$3,$4,$5,$6,'active',$7)
			ON CONFLICT(space_id,connected_by_user_id,provider,display_name) DO UPDATE SET credential_reference=EXCLUDED.credential_reference,granted_permissions=EXCLUDED.granted_permissions,status='active',updated_at=NOW()
			WHERE space_integrations.connected_by_user_id=EXCLUDED.connected_by_user_id`, item.IntegrationID, item.SpaceID, item.Provider, displayName, item.ID, permissions, item.UserID); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `SELECT id,created_at,updated_at FROM space_integrations WHERE space_id=$1 AND provider=$2 AND display_name=$3 AND connected_by_user_id=$4`, item.SpaceID, item.Provider, displayName, item.UserID).Scan(&out.ID, &out.CreatedAt, &out.UpdatedAt); err != nil {
			return err
		}
		item.IntegrationID = out.ID
		_, err := tx.ExecContext(ctx, `INSERT INTO space_provider_credentials(id,integration_id,space_id,user_id,provider,ciphertext,nonce,key_version,account_id,account_display,expires_at,last_refreshed_at)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,NOW())
			ON CONFLICT(integration_id) DO UPDATE SET ciphertext=EXCLUDED.ciphertext,nonce=EXCLUDED.nonce,key_version=EXCLUDED.key_version,account_id=EXCLUDED.account_id,account_display=EXCLUDED.account_display,expires_at=EXCLUDED.expires_at,last_refreshed_at=NOW(),revoked_at=NULL,updated_at=NOW()`, item.ID, item.IntegrationID, item.SpaceID, item.UserID, item.Provider, item.Ciphertext, item.Nonce, item.KeyVersion, item.AccountID, item.AccountDisplay, item.ExpiresAt)
		return err
	})
	return out, err
}

func (db *Database) ProviderCredential(ctx context.Context, userID, spaceID, integrationID string) (*ProviderCredential, error) {
	out := &ProviderCredential{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		return tx.QueryRowContext(ctx, `SELECT c.id,c.integration_id,c.space_id,c.user_id,c.provider,c.ciphertext,c.nonce,c.key_version,c.account_id,c.account_display,c.expires_at
			FROM space_provider_credentials c JOIN space_integrations i ON i.id=c.integration_id
			WHERE c.integration_id=$1 AND c.space_id=$3 AND c.revoked_at IS NULL AND i.status='active'
			  AND (
			    c.user_id=$2
			    OR EXISTS(SELECT 1 FROM spaces s WHERE s.id=$3 AND s.owner_user_id=$2)
			    OR EXISTS(SELECT 1 FROM provider_shared_resources r
			      WHERE r.integration_id=c.integration_id AND r.space_id=$3 AND r.status='active')
			  )`, integrationID, userID, spaceID).
			Scan(&out.ID, &out.IntegrationID, &out.SpaceID, &out.UserID, &out.Provider, &out.Ciphertext, &out.Nonce, &out.KeyVersion, &out.AccountID, &out.AccountDisplay, &out.ExpiresAt)
	})
	if err == sql.ErrNoRows {
		return nil, ErrSpaceNotFound
	}
	return out, err
}

func (db *Database) UpdateProviderCredentialSecret(ctx context.Context, item ProviderCredential) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE space_provider_credentials SET ciphertext=$1,nonce=$2,key_version=$3,expires_at=$4,last_refreshed_at=NOW(),updated_at=NOW()
			WHERE id=$5 AND integration_id=$6 AND user_id=$7 AND space_id=$8 AND revoked_at IS NULL`, item.Ciphertext, item.Nonce, item.KeyVersion, item.ExpiresAt, item.ID, item.IntegrationID, item.UserID, item.SpaceID)
		if err != nil {
			return err
		}
		if changed, _ := result.RowsAffected(); changed != 1 {
			return ErrSpaceNotFound
		}
		return nil
	})
}

func (db *Database) DeleteProviderIntegration(ctx context.Context, userID, integrationID string) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		var spaceID string
		if err := tx.QueryRowContext(ctx, `SELECT space_id FROM space_integrations WHERE id=$1`, integrationID).Scan(&spaceID); err != nil {
			return ErrSpaceNotFound
		}
		if err := requireSpaceOwnerTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `DELETE FROM space_integrations WHERE id=$1 AND space_id=$2`, integrationID, spaceID)
		if err != nil {
			return err
		}
		if changed, _ := result.RowsAffected(); changed == 0 {
			return ErrSpaceNotFound
		}
		return nil
	})
}
