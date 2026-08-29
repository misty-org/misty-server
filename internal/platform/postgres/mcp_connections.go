package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

type MCPRemoteConnection struct {
	ID               string     `json:"id"`
	OwnerUserID      string     `json:"-"`
	Name             string     `json:"name"`
	EndpointURL      string     `json:"endpoint_url"`
	Transport        string     `json:"transport"`
	Provider         string     `json:"provider"`
	BearerCiphertext []byte     `json:"-"`
	BearerNonce      []byte     `json:"-"`
	KeyVersion       int        `json:"-"`
	Status           string     `json:"status"`
	LastErrorCode    string     `json:"last_error_code,omitempty"`
	LastCheckedAt    *time.Time `json:"last_checked_at,omitempty"`
	LastDiscoveredAt *time.Time `json:"last_discovered_at,omitempty"`
	ToolCount        int        `json:"tool_count"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

const mcpConnectionColumns = `c.id,c.owner_user_id,c.name,c.endpoint_url,c.transport,c.provider,c.bearer_ciphertext,c.bearer_nonce,c.key_version,c.status,c.last_error_code,c.last_checked_at,c.last_discovered_at,(SELECT COUNT(*) FROM mcp_remote_tools t WHERE t.connection_id=c.id AND t.removed_at IS NULL),c.created_at,c.updated_at`

func scanMCPConnection(row scanner, item *MCPRemoteConnection) error {
	return row.Scan(&item.ID, &item.OwnerUserID, &item.Name, &item.EndpointURL, &item.Transport, &item.Provider, &item.BearerCiphertext, &item.BearerNonce, &item.KeyVersion, &item.Status, &item.LastErrorCode, &item.LastCheckedAt, &item.LastDiscoveredAt, &item.ToolCount, &item.CreatedAt, &item.UpdatedAt)
}

func (db *Database) CreateMCPRemoteConnection(ctx context.Context, item MCPRemoteConnection) (*MCPRemoteConnection, error) {
	item.ID = "mcp_connection_" + uuid.NewString()
	item.Name, item.EndpointURL = strings.TrimSpace(item.Name), strings.TrimSpace(item.EndpointURL)
	if item.OwnerUserID == "" || item.Name == "" || len(item.Name) > 120 || item.EndpointURL == "" || len(item.EndpointURL) > 2048 {
		return nil, ErrSpaceInvalid
	}
	if item.KeyVersion < 1 {
		item.KeyVersion = 1
	}
	if item.Provider == "" {
		item.Provider = "custom"
	}
	if item.Provider != "custom" && item.Provider != "activepieces" {
		return nil, ErrSpaceInvalid
	}
	out := &MCPRemoteConnection{}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(item.OwnerUserID), func(tx *sql.Tx) error {
		return scanMCPConnection(tx.QueryRowContext(ctx, `INSERT INTO mcp_remote_connections AS c(id,owner_user_id,name,endpoint_url,provider,bearer_ciphertext,bearer_nonce,key_version)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8)
			ON CONFLICT(owner_user_id,endpoint_url) DO UPDATE SET
				name=EXCLUDED.name,provider=EXCLUDED.provider,bearer_ciphertext=EXCLUDED.bearer_ciphertext,
				bearer_nonce=EXCLUDED.bearer_nonce,key_version=EXCLUDED.key_version,status='unchecked',
				last_error_code='',revoked_at=NULL,updated_at=NOW()
			RETURNING `+mcpConnectionColumns,
			item.ID, item.OwnerUserID, item.Name, item.EndpointURL, item.Provider, item.BearerCiphertext, item.BearerNonce, item.KeyVersion), out)
	})
	return out, err
}

type MCPOAuthCredential struct {
	ConnectionID         string
	OwnerUserID          string
	CredentialCiphertext []byte
	CredentialNonce      []byte
	KeyVersion           int16
	ExpiresAt            *time.Time
}

type MCPOAuthState struct {
	OwnerUserID      string
	SecretCiphertext []byte
	SecretNonce      []byte
	ExpiresAt        time.Time
}

func (db *Database) CreateMCPOAuthState(ctx context.Context, stateHash string, item MCPOAuthState) error {
	return db.TestingWithRLSContext(ctx, userRLSSettings(item.OwnerUserID), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO mcp_oauth_states(state_hash,owner_user_id,secret_ciphertext,secret_nonce,expires_at) VALUES($1,$2,$3,$4,$5)`, stateHash, item.OwnerUserID, item.SecretCiphertext, item.SecretNonce, item.ExpiresAt)
		return err
	})
}

func (db *Database) ConsumeMCPOAuthState(ctx context.Context, stateHash string) (*MCPOAuthState, error) {
	item := &MCPOAuthState{}
	err := db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `UPDATE mcp_oauth_states SET consumed_at=NOW() WHERE state_hash=$1 AND consumed_at IS NULL AND expires_at>NOW() RETURNING owner_user_id,secret_ciphertext,secret_nonce,expires_at`, stateHash).Scan(&item.OwnerUserID, &item.SecretCiphertext, &item.SecretNonce, &item.ExpiresAt)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceNotFound
	}
	return item, err
}

func (db *Database) SaveMCPOAuthCredential(ctx context.Context, item MCPOAuthCredential) error {
	return db.TestingWithRLSContext(ctx, userRLSSettings(item.OwnerUserID), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO mcp_oauth_credentials(connection_id,owner_user_id,credential_ciphertext,credential_nonce,key_version,expires_at)
			VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(connection_id) DO UPDATE SET credential_ciphertext=EXCLUDED.credential_ciphertext,credential_nonce=EXCLUDED.credential_nonce,key_version=EXCLUDED.key_version,expires_at=EXCLUDED.expires_at,updated_at=NOW()`, item.ConnectionID, item.OwnerUserID, item.CredentialCiphertext, item.CredentialNonce, item.KeyVersion, item.ExpiresAt)
		return err
	})
}

func (db *Database) MCPOAuthCredential(ctx context.Context, userID, connectionID string) (*MCPOAuthCredential, error) {
	item := &MCPOAuthCredential{}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT connection_id,owner_user_id,credential_ciphertext,credential_nonce,key_version,expires_at FROM mcp_oauth_credentials WHERE connection_id=$1 AND owner_user_id=$2`, connectionID, userID).Scan(&item.ConnectionID, &item.OwnerUserID, &item.CredentialCiphertext, &item.CredentialNonce, &item.KeyVersion, &item.ExpiresAt)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceNotFound
	}
	return item, err
}

func (db *Database) UpdateMCPOAuthToken(ctx context.Context, item MCPOAuthCredential, bearerCiphertext, bearerNonce []byte) error {
	return db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE mcp_oauth_credentials SET credential_ciphertext=$1,credential_nonce=$2,key_version=$3,expires_at=$4,updated_at=NOW() WHERE connection_id=$5 AND owner_user_id=$6`, item.CredentialCiphertext, item.CredentialNonce, item.KeyVersion, item.ExpiresAt, item.ConnectionID, item.OwnerUserID); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE mcp_remote_connections SET bearer_ciphertext=$1,bearer_nonce=$2,key_version=$3,status='active',last_error_code='',updated_at=NOW() WHERE id=$4 AND owner_user_id=$5 AND revoked_at IS NULL`, bearerCiphertext, bearerNonce, item.KeyVersion, item.ConnectionID, item.OwnerUserID)
		if err != nil {
			return err
		}
		if changed, _ := result.RowsAffected(); changed != 1 {
			return ErrSpaceNotFound
		}
		return nil
	})
}

func (db *Database) MCPRemoteConnections(ctx context.Context, userID string) ([]MCPRemoteConnection, error) {
	items := []MCPRemoteConnection{}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT `+mcpConnectionColumns+` FROM mcp_remote_connections c WHERE c.owner_user_id=$1 AND c.revoked_at IS NULL ORDER BY c.updated_at DESC,c.id`, userID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item MCPRemoteConnection
			if err := scanMCPConnection(rows, &item); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}

func (db *Database) MCPRemoteConnection(ctx context.Context, userID, connectionID string) (*MCPRemoteConnection, error) {
	item := &MCPRemoteConnection{}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		return scanMCPConnection(tx.QueryRowContext(ctx, `SELECT `+mcpConnectionColumns+` FROM mcp_remote_connections c WHERE c.id=$1 AND c.owner_user_id=$2 AND c.revoked_at IS NULL`, connectionID, userID), item)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceNotFound
	}
	return item, err
}

func (db *Database) SetMCPConnectionHealth(ctx context.Context, userID, connectionID, status, errorCode string, discovered bool) (*MCPRemoteConnection, error) {
	if status != "active" && status != "needs_attention" {
		return nil, ErrSpaceInvalid
	}
	item := &MCPRemoteConnection{}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		return scanMCPConnection(tx.QueryRowContext(ctx, `UPDATE mcp_remote_connections c SET status=$1,last_error_code=$2,last_checked_at=NOW(),last_discovered_at=CASE WHEN $3 THEN NOW() ELSE last_discovered_at END,updated_at=NOW() WHERE id=$4 AND owner_user_id=$5 AND revoked_at IS NULL RETURNING `+mcpConnectionColumns, status, errorCode, discovered, connectionID, userID), item)
	})
	return item, err
}

func (db *Database) RevokeMCPRemoteConnection(ctx context.Context, userID, connectionID string) error {
	return db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE mcp_remote_connections SET bearer_ciphertext=''::bytea,bearer_nonce=''::bytea,status='revoked',last_error_code='',revoked_at=NOW(),updated_at=NOW() WHERE id=$1 AND owner_user_id=$2 AND revoked_at IS NULL`, connectionID, userID)
		if err != nil {
			return err
		}
		if changed, _ := result.RowsAffected(); changed != 1 {
			return ErrSpaceNotFound
		}
		if _, err := tx.ExecContext(ctx, `UPDATE personal_agent_mcp_tools SET enabled=FALSE,updated_at=NOW() WHERE connection_id=$1`, connectionID); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE mcp_remote_tools SET removed_at=COALESCE(removed_at,NOW()),updated_at=NOW() WHERE connection_id=$1`, connectionID)
		return err
	})
}
