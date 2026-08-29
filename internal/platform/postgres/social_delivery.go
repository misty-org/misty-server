package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

func (db *Database) QueueDueSocialScheduledMessages(ctx context.Context, limit int) (int, error) {
	if limit < 1 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	count := 0
	err := db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT s.id,s.space_id,s.binding_id,s.conversation_id,s.authority_id,s.created_by_user_id,s.content FROM social_scheduled_messages s JOIN social_send_authorities a ON a.id=s.authority_id WHERE s.status='scheduled' AND s.scheduled_at<=NOW() AND a.allow_scheduled AND a.revoked_at IS NULL ORDER BY s.scheduled_at FOR UPDATE OF s SKIP LOCKED LIMIT $1`, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		type due struct {
			id, space, binding, conversation, authority, user string
			content                                           []byte
		}
		items := []due{}
		for rows.Next() {
			var i due
			if err := rows.Scan(&i.id, &i.space, &i.binding, &i.conversation, &i.authority, &i.user, &i.content); err != nil {
				return err
			}
			items = append(items, i)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		for _, i := range items {
			commandID := "social_command_" + uuid.NewString()
			key := "scheduled:" + i.id
			if _, err := tx.ExecContext(ctx, `INSERT INTO social_outbound_commands(id,space_id,binding_id,conversation_id,authority_id,requested_by_user_id,source_kind,content,idempotency_key) VALUES($1,$2,$3,$4,$5,$6,'scheduled',$7,$8) ON CONFLICT(idempotency_key) DO NOTHING`, commandID, i.space, i.binding, i.conversation, i.authority, i.user, i.content, key); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE social_scheduled_messages SET status='queued',outbound_command_id=$1,updated_at=NOW() WHERE id=$2`, commandID, i.id); err != nil {
				return err
			}
			count++
		}
		return nil
	})
	return count, err
}

func (db *Database) SocialManualSendContext(ctx context.Context, userID, spaceID, conversationID string) (*SocialBinding, *SocialSendAuthority, error) {
	binding := &SocialBinding{}
	authority := &SocialSendAuthority{}
	bindingFound := false
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionMessagesWrite); err != nil {
			return err
		}
		if err := scanSocialBinding(tx.QueryRowContext(ctx, `SELECT `+socialBindingColumns+` FROM social_bindings WHERE space_id=$1 AND conversation_id=$2 AND status='active' AND disabled_at IS NULL`, spaceID, conversationID), binding); err != nil {
			return err
		}
		bindingFound = true
		return tx.QueryRowContext(ctx, `SELECT id,space_id,user_id,connection_id,COALESCE(binding_id,''),allow_manual,allow_scheduled,allow_automation,hourly_limit,daily_limit,quiet_hours,timezone,approved_at,revoked_at FROM social_send_authorities WHERE user_id=$1 AND connection_id=$2 AND (binding_id=$3 OR binding_id IS NULL) AND allow_manual AND revoked_at IS NULL ORDER BY binding_id NULLS LAST LIMIT 1`, userID, binding.ConnectionID, binding.ID).Scan(&authority.ID, &authority.SpaceID, &authority.UserID, &authority.ConnectionID, &authority.BindingID, &authority.AllowManual, &authority.AllowScheduled, &authority.AllowAutomation, &authority.HourlyLimit, &authority.DailyLimit, &authority.QuietHours, &authority.Timezone, &authority.ApprovedAt, &authority.RevokedAt)
	})
	if errors.Is(err, sql.ErrNoRows) {
		if !bindingFound {
			return nil, nil, ErrSpaceNotFound
		}
		return binding, nil, ErrSpaceForbidden
	}
	return binding, authority, err
}

func (db *Database) QueueSocialManualCommand(ctx context.Context, userID, spaceID, conversationID string, content []MessageSpan, clientNonce string) (*SocialOutboundCommand, error) {
	binding, authority, err := db.SocialManualSendContext(ctx, userID, spaceID, conversationID)
	if err != nil {
		return nil, err
	}
	if clientNonce == "" {
		clientNonce = uuid.NewString()
	}
	item := &SocialOutboundCommand{ID: "social_command_" + uuid.NewString(), SpaceID: spaceID, BindingID: binding.ID, ConversationID: conversationID, AuthorityID: authority.ID, RequestedByUserID: userID, SourceKind: "manual", Content: mustJSON(content), IdempotencyKey: "manual:" + spaceID + ":" + clientNonce, State: "queued"}
	err = db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `INSERT INTO social_outbound_commands(id,space_id,binding_id,conversation_id,authority_id,requested_by_user_id,source_kind,content,idempotency_key) VALUES($1,$2,$3,$4,$5,$6,'manual',$7,$8) ON CONFLICT(idempotency_key) DO UPDATE SET idempotency_key=EXCLUDED.idempotency_key RETURNING id,state,available_at,created_at,updated_at`, item.ID, spaceID, item.BindingID, conversationID, item.AuthorityID, userID, item.Content, item.IdempotencyKey).Scan(&item.ID, &item.State, &item.AvailableAt, &item.CreatedAt, &item.UpdatedAt)
	})
	return item, err
}

func (db *Database) ClaimSocialOutboundCommands(ctx context.Context, workerID string, limit int) ([]SocialOutboundDelivery, error) {
	if limit < 1 {
		limit = 10
	}
	if limit > 50 {
		limit = 50
	}
	items := []SocialOutboundDelivery{}
	err := db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `WITH ready AS (SELECT id FROM social_outbound_commands WHERE state='queued' AND available_at<=NOW() AND (lease_expires_at IS NULL OR lease_expires_at<NOW()) ORDER BY available_at,created_at FOR UPDATE SKIP LOCKED LIMIT $1), claimed AS (UPDATE social_outbound_commands c SET state='sending',attempts=attempts+1,lease_expires_at=NOW()+INTERVAL '60 seconds',updated_at=NOW() FROM ready WHERE c.id=ready.id RETURNING c.*) SELECT c.id,c.space_id,c.binding_id,c.conversation_id,COALESCE(c.authority_id,''),COALESCE(c.requested_by_user_id,''),c.source_kind,c.content,c.idempotency_key,c.state,c.attempts,c.available_at,c.lease_expires_at,c.provider_receipt,c.last_error_code,c.created_at,c.updated_at,b.provider,b.external_resource_id,b.external_parent_id,b.connection_id,b.connected_by_user_id FROM claimed c JOIN social_bindings b ON b.id=c.binding_id WHERE b.status='active' AND b.disabled_at IS NULL`, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var i SocialOutboundDelivery
			if err := rows.Scan(&i.ID, &i.SpaceID, &i.BindingID, &i.ConversationID, &i.AuthorityID, &i.RequestedByUserID, &i.SourceKind, &i.Content, &i.IdempotencyKey, &i.State, &i.Attempts, &i.AvailableAt, &i.LeaseExpiresAt, &i.ProviderReceipt, &i.LastErrorCode, &i.CreatedAt, &i.UpdatedAt, &i.Provider, &i.ExternalResourceID, &i.ExternalParentID, &i.ConnectionID, &i.ConnectionUserID); err != nil {
				return err
			}
			items = append(items, i)
		}
		return rows.Err()
	})
	return items, err
}

func (db *Database) CompleteSocialOutboundCommand(ctx context.Context, id, externalID string, receipt json.RawMessage) error {
	return db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE social_outbound_commands SET state='sent',provider_receipt=$2,last_error_code='',lease_expires_at=NULL,updated_at=NOW() WHERE id=$1`, id, receipt)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE social_scheduled_messages SET status='sent',updated_at=NOW() WHERE outbound_command_id=$1`, id)
		return err
	})
}

func (db *Database) FailSocialOutboundCommand(ctx context.Context, id, errorCode string, retry bool) error {
	return db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		state := "failed"
		delay := time.Duration(0)
		if retry {
			state = "queued"
			delay = time.Minute
		}
		_, err := tx.ExecContext(ctx, `UPDATE social_outbound_commands SET state=$2,last_error_code=$3,available_at=NOW()+$4::interval,lease_expires_at=NULL,updated_at=NOW() WHERE id=$1`, id, state, errorCode, delay.String())
		if err != nil {
			return err
		}
		if !retry {
			_, err = tx.ExecContext(ctx, `UPDATE social_scheduled_messages SET status='failed',last_error_code=$2,updated_at=NOW() WHERE outbound_command_id=$1`, id, errorCode)
		}
		return err
	})
}
