package db

import (
	"context"
	"database/sql"
	"time"

	"github.com/google/uuid"
)

func (db *Database) BeginFigmaWebhookDelivery(ctx context.Context, hash, subscriptionID, webhookID, eventType, fileKey string, occurredAt *time.Time) (bool, error) {
	claimed := false
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `INSERT INTO figma_webhook_deliveries
			(delivery_hash,subscription_id,webhook_id,event_type,file_key,event_timestamp)
			VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(delivery_hash) DO NOTHING`,
			hash, subscriptionID, webhookID, eventType, fileKey, occurredAt)
		if err != nil {
			return err
		}
		if changed, _ := result.RowsAffected(); changed == 1 {
			claimed = true
			return nil
		}
		result, err = tx.ExecContext(ctx, `UPDATE figma_webhook_deliveries SET
			state='processing',error_code='',received_at=NOW(),processed_at=NULL,
			subscription_id=$2,webhook_id=$3,event_type=$4,file_key=$5,event_timestamp=$6
			WHERE delivery_hash=$1 AND (state='failed' OR (state='processing' AND received_at<NOW()-INTERVAL '5 minutes'))`,
			hash, subscriptionID, webhookID, eventType, fileKey, occurredAt)
		if err != nil {
			return err
		}
		changed, _ := result.RowsAffected()
		claimed = changed == 1
		return nil
	})
	return claimed, err
}

func (db *Database) FinishFigmaWebhookDelivery(ctx context.Context, hash, state, errorCode string) error {
	if !oneOf(state, "processed", "ignored", "failed") {
		return ErrSpaceInvalid
	}
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE figma_webhook_deliveries SET state=$2,error_code=$3,processed_at=NOW() WHERE delivery_hash=$1`, hash, state, errorCode)
		return err
	})
}

func (db *Database) RecordFigmaCommentAudit(ctx context.Context, userID, spaceID, bindingID, source, fileKey, nodeID, errorCode string, confirmed, success bool) error {
	if !oneOf(source, "user", "agent") {
		return ErrSpaceInvalid
	}
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO figma_comment_audit
			(id,space_id,binding_id,actor_user_id,source,file_key,target_node_id,confirmed,success,error_code)
			VALUES($1,$2,$3,NULLIF($4,''),$5,$6,$7,$8,$9,$10)`,
			"figma_audit_"+uuid.NewString(), spaceID, bindingID, userID, source, fileKey, nodeID, confirmed, success, errorCode)
		return err
	})
}

func (db *Database) ClaimFigmaCommentAction(ctx context.Context, userID, spaceID, bindingID, source, fileKey, nodeID, idempotencyKey, fingerprint string) (bool, error) {
	if !oneOf(source, "user", "agent") || idempotencyKey == "" || len(idempotencyKey) > 200 || len(fingerprint) != 64 {
		return false, ErrSpaceInvalid
	}
	claimed := false
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `INSERT INTO figma_comment_audit
			(id,space_id,binding_id,actor_user_id,source,idempotency_key,action_fingerprint,file_key,target_node_id,confirmed,success,error_code)
			VALUES($1,$2,$3,NULLIF($4,''),$5,$6,$7,$8,$9,true,false,'pending') ON CONFLICT DO NOTHING`,
			"figma_audit_"+uuid.NewString(), spaceID, bindingID, userID, source, idempotencyKey, fingerprint, fileKey, nodeID)
		if err != nil {
			return err
		}
		changed, _ := result.RowsAffected()
		claimed = changed == 1
		return nil
	})
	return claimed, err
}

func (db *Database) FinishFigmaCommentAction(ctx context.Context, bindingID, idempotencyKey, errorCode string, success bool) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE figma_comment_audit SET success=$3,error_code=$4
			WHERE binding_id=$1 AND idempotency_key=$2`, bindingID, idempotencyKey, success, errorCode)
		return err
	})
}
