package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

func (db *Database) RecordAccountDeletionFailure(
	ctx context.Context, requestID, code string,
) error {
	if strings.TrimSpace(code) == "" {
		code = "cleanup_failed"
	}
	return db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			UPDATE account_deletion_requests
			SET last_error_code=$1,updated_at=NOW()
			WHERE id=$2 AND status='processing'`, code, requestID)
		return err
	})
}

func (db *Database) ScheduleAccountDeletion(
	ctx context.Context, requestID string, providerStatus map[string]string,
) error {
	raw, err := json.Marshal(providerStatus)
	if err != nil {
		return err
	}
	return db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		var userID string
		if err := tx.QueryRowContext(ctx, `
			SELECT user_id FROM account_deletion_requests
			WHERE id=$1 AND status='processing' FOR UPDATE`,
			requestID,
		).Scan(&userID); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `
			SELECT space_id FROM space_members
			WHERE user_id=$1 AND role='member'`, userID)
		if err != nil {
			return err
		}
		spaceIDs := []string{}
		for rows.Next() {
			var spaceID string
			if err := rows.Scan(&spaceID); err != nil {
				rows.Close()
				return err
			}
			spaceIDs = append(spaceIDs, spaceID)
		}
		if err := rows.Close(); err != nil {
			return err
		}
		for _, spaceID := range spaceIDs {
			if _, err := tx.ExecContext(ctx, `
				DELETE FROM space_conversation_members cm
				USING space_conversations c
				WHERE cm.conversation_id=c.id AND c.space_id=$1
				  AND cm.user_id=$2`, spaceID, userID); err != nil {
				return err
			}
			if err := handleNoteMembershipLossTx(ctx, tx, spaceID, userID); err != nil {
				return err
			}
			if err := revokeDrawingAccessForSpaceTx(ctx, tx, spaceID); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `
				DELETE FROM space_members WHERE space_id=$1 AND user_id=$2`,
				spaceID, userID,
			); err != nil {
				return err
			}
			if err := notifySpaceControlTx(ctx, tx, map[string]any{
				"type": "member.left", "space_id": spaceID,
				"user_ids": []string{userID},
			}); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE cloud_connections
			SET revoked_at=NOW(),credential_ciphertext=''::bytea,
			    credential_nonce=''::bytea,updated_at=NOW()
			WHERE user_id=$1`, userID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM cloud_oauth_states WHERE user_id=$1`, userID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE space_integrations
			SET status='needs_attention',credential_reference='account_deleted',
			    updated_at=NOW()
			WHERE connected_by_user_id=$1`, userID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE space_provider_credentials
			SET revoked_at=NOW(),ciphertext=''::bytea,nonce=''::bytea,
			    updated_at=NOW()
			WHERE user_id=$1`, userID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM provider_oauth_states WHERE user_id=$1`, userID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE provider_subscriptions
			SET status='disabled',updated_at=NOW()
			WHERE user_id=$1`, userID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM provider_event_inbox WHERE user_id=$1`, userID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE workflow_device_node_jobs
			SET state='canceled',completed_at=NOW()
			WHERE user_id=$1 AND state IN ('queued','leased')`, userID); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `
			UPDATE account_deletion_requests
			SET status='scheduled',provider_revocation_status=$1,
			    updated_at=NOW(),last_error_code=''
			WHERE id=$2`, raw, requestID)
		return err
	})
}

func (db *Database) DueAccountDeletions(
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
			WHERE status='scheduled' AND purge_after<=NOW()
			ORDER BY purge_after FOR UPDATE SKIP LOCKED LIMIT $1`, limit)
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

func (db *Database) CompleteAccountDeletion(
	ctx context.Context, requestID string,
) error {
	return db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		var userID string
		if err := tx.QueryRowContext(ctx, `
			SELECT user_id FROM account_deletion_requests
			WHERE id=$1 AND status='scheduled' AND purge_after<=NOW()
			FOR UPDATE`, requestID,
		).Scan(&userID); err != nil {
			return err
		}
		suffix := strings.ReplaceAll(userID, "-", "")
		if len(suffix) > 16 {
			suffix = suffix[:16]
		}
		password, err := bcrypt.GenerateFromPassword(
			[]byte(uuid.NewString()+uuid.NewString()), bcrypt.DefaultCost,
		)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id=$1`, userID); err != nil {
			return err
		}
		if err := purgeAccountAgentsTx(ctx, tx, userID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE users
			SET name='Deleted user',username=$1,email=$2,password_hash=$3,
			    email_updates_enabled=FALSE,analytics_enabled=FALSE,
			    error_reporting_enabled=FALSE,avatar_version=0,
			    lifecycle_state='deleted',anonymized_at=NOW()
			WHERE id=$4 AND lifecycle_state='pending_deletion'`,
			"deleted_"+suffix, "deleted+"+suffix+"@misty.invalid",
			string(password), userID,
		); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `
			UPDATE account_deletion_requests
			SET status='completed',completed_at=NOW(),updated_at=NOW()
			WHERE id=$1`, requestID)
		return err
	})
}

func scanAccountDeletionRequest(
	scanner interface{ Scan(...any) error }, out *AccountDeletionRequest,
) error {
	var providerStatus []byte
	if err := scanner.Scan(
		&out.ID, &out.UserID, &out.Status, &out.PurgeAfter,
		&providerStatus, &out.LastErrorCode, &out.CreatedAt,
		&out.UpdatedAt, &out.CompletedAt,
	); err != nil {
		return err
	}
	out.ProviderRevocationStatus = map[string]string{}
	return json.Unmarshal(providerStatus, &out.ProviderRevocationStatus)
}
