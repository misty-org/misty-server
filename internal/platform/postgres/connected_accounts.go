package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

type ConnectedAccount struct {
	ID, UserID, Provider, AccountID, AccountDisplay string
	CredentialCiphertext, CredentialNonce           []byte
	KeyVersion                                      int16
	Capabilities, GrantedScopes                     []string
	Status, LastErrorCode                           string
	ExpiresAt, RevokedAt                            *time.Time
	CreatedAt, UpdatedAt                            time.Time
}

type ConnectedAccountOAuthState struct {
	UserID, Provider, ReturnTo        string
	Capabilities, RequestedScopes     []string
	VerifierCiphertext, VerifierNonce []byte
	ExpiresAt                         time.Time
}

const connectedAccountColumns = `id,user_id,provider,account_id,account_display,
	credential_ciphertext,credential_nonce,key_version,capabilities,granted_scopes,
	status,last_error_code,expires_at,revoked_at,created_at,updated_at`

func scanConnectedAccount(row interface{ Scan(...any) error }, item *ConnectedAccount) error {
	var capabilities, scopes []byte
	if err := row.Scan(&item.ID, &item.UserID, &item.Provider, &item.AccountID,
		&item.AccountDisplay, &item.CredentialCiphertext, &item.CredentialNonce,
		&item.KeyVersion, &capabilities, &scopes, &item.Status, &item.LastErrorCode,
		&item.ExpiresAt, &item.RevokedAt, &item.CreatedAt, &item.UpdatedAt); err != nil {
		return err
	}
	_ = json.Unmarshal(capabilities, &item.Capabilities)
	_ = json.Unmarshal(scopes, &item.GrantedScopes)
	return nil
}

func (db *Database) ConnectedAccounts(ctx context.Context, userID string) ([]ConnectedAccount, error) {
	items := []ConnectedAccount{}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT `+connectedAccountColumns+`
			FROM connected_accounts WHERE user_id=$1 AND revoked_at IS NULL
			ORDER BY provider,account_display,id`, userID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item ConnectedAccount
			if err := scanConnectedAccount(rows, &item); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}

func (db *Database) ConnectedAccount(ctx context.Context, userID, id string) (*ConnectedAccount, error) {
	item := &ConnectedAccount{}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		return scanConnectedAccount(tx.QueryRowContext(ctx, `SELECT `+connectedAccountColumns+`
			FROM connected_accounts WHERE id=$1 AND user_id=$2 AND revoked_at IS NULL`, id, userID), item)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceNotFound
	}
	return item, err
}

func (db *Database) ConnectedAccountByIdentity(ctx context.Context, userID, provider, accountID string) (*ConnectedAccount, error) {
	item := &ConnectedAccount{}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		return scanConnectedAccount(tx.QueryRowContext(ctx, `SELECT `+connectedAccountColumns+`
			FROM connected_accounts WHERE user_id=$1 AND provider=$2 AND account_id=$3`,
			userID, provider, accountID), item)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceNotFound
	}
	return item, err
}

func (db *Database) SaveConnectedAccount(ctx context.Context, item ConnectedAccount) (*ConnectedAccount, error) {
	if item.ID == "" {
		item.ID = "connection_" + uuid.NewString()
	}
	if item.Status == "" {
		item.Status = "active"
	}
	if item.Capabilities == nil {
		item.Capabilities = []string{}
	}
	if item.GrantedScopes == nil {
		item.GrantedScopes = []string{}
	}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(item.UserID), func(tx *sql.Tx) error {
		query := `INSERT INTO connected_accounts
			(id,user_id,provider,account_id,account_display,credential_ciphertext,credential_nonce,
			 key_version,capabilities,granted_scopes,status,last_error_code,expires_at,last_refreshed_at)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'active','',$11,NOW())
			ON CONFLICT(user_id,provider,account_id) DO UPDATE SET
			 account_display=EXCLUDED.account_display,
			 credential_ciphertext=EXCLUDED.credential_ciphertext,
			 credential_nonce=EXCLUDED.credential_nonce,key_version=EXCLUDED.key_version,
			 capabilities=EXCLUDED.capabilities,granted_scopes=EXCLUDED.granted_scopes,
			 status='active',last_error_code='',expires_at=EXCLUDED.expires_at,
			 last_refreshed_at=NOW(),revoked_at=NULL,updated_at=NOW()
			RETURNING ` + connectedAccountColumns
		return scanConnectedAccount(tx.QueryRowContext(ctx, query, item.ID, item.UserID,
			item.Provider, item.AccountID, item.AccountDisplay, item.CredentialCiphertext,
			item.CredentialNonce, item.KeyVersion, mustJSON(item.Capabilities),
			mustJSON(item.GrantedScopes), item.ExpiresAt), &item)
	})
	return &item, err
}

func (db *Database) UpdateConnectedAccountCredential(ctx context.Context, item ConnectedAccount) error {
	return db.TestingWithRLSContext(ctx, userRLSSettings(item.UserID), func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE connected_accounts SET
			credential_ciphertext=$1,credential_nonce=$2,key_version=$3,expires_at=$4,
			status='active',last_error_code='',last_refreshed_at=NOW(),updated_at=NOW()
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

func (db *Database) SetConnectedAccountHealth(ctx context.Context, userID, id, status, errorCode string) error {
	if status != "active" && status != "needs_attention" {
		return ErrSpaceInvalid
	}
	return db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE connected_accounts
			SET status=$1,last_error_code=$2,updated_at=NOW()
			WHERE id=$3 AND user_id=$4 AND revoked_at IS NULL`, status, errorCode, id, userID)
		if err != nil {
			return err
		}
		if changed, _ := result.RowsAffected(); changed != 1 {
			return ErrSpaceNotFound
		}
		return nil
	})
}

func (db *Database) RevokeConnectedAccount(ctx context.Context, userID, id string) error {
	return db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE connected_accounts SET
			credential_ciphertext=''::bytea,credential_nonce=''::bytea,status='revoked',
			last_error_code='',revoked_at=NOW(),updated_at=NOW()
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

func (db *Database) CreateConnectedAccountOAuthState(ctx context.Context, stateHash string, item ConnectedAccountOAuthState) error {
	return db.TestingWithRLSContext(ctx, userRLSSettings(item.UserID), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO connected_account_oauth_states
			(state_hash,user_id,provider,capabilities,requested_scopes,verifier_ciphertext,
			 verifier_nonce,return_to,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
			stateHash, item.UserID, item.Provider, mustJSON(item.Capabilities),
			mustJSON(item.RequestedScopes), item.VerifierCiphertext, item.VerifierNonce,
			item.ReturnTo, item.ExpiresAt)
		return err
	})
}

func (db *Database) ConsumeConnectedAccountOAuthState(ctx context.Context, stateHash string) (*ConnectedAccountOAuthState, error) {
	item := &ConnectedAccountOAuthState{}
	err := db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		var capabilities, scopes []byte
		err := tx.QueryRowContext(ctx, `UPDATE connected_account_oauth_states SET consumed_at=NOW()
			WHERE state_hash=$1 AND consumed_at IS NULL AND expires_at>NOW()
			RETURNING user_id,provider,capabilities,requested_scopes,verifier_ciphertext,
			 verifier_nonce,return_to,expires_at`, stateHash).Scan(&item.UserID, &item.Provider,
			&capabilities, &scopes, &item.VerifierCiphertext, &item.VerifierNonce,
			&item.ReturnTo, &item.ExpiresAt)
		_ = json.Unmarshal(capabilities, &item.Capabilities)
		_ = json.Unmarshal(scopes, &item.RequestedScopes)
		return err
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceNotFound
	}
	return item, err
}
