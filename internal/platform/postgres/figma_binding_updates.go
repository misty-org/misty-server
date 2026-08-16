package db

import (
	"context"
	"database/sql"
	"errors"
	"github.com/google/uuid"
)

func (db *Database) SetFigmaBindingSync(ctx context.Context, id, cursor, status, errorCode string) error {
	if !oneOf(status, "active", "needs_attention") {
		return ErrSpaceInvalid
	}
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE figma_space_bindings SET sync_cursor=$2,status=$3,last_error_code=$4,last_synced_at=NOW(),updated_at=NOW() WHERE id=$1 AND disabled_at IS NULL`, id, cursor, status, errorCode)
		return err
	})
}
func (db *Database) MarkFigmaBindingUpdateAvailable(ctx context.Context, id string) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE figma_space_bindings SET status='needs_attention',last_error_code='figma_update_available',updated_at=NOW() WHERE id=$1 AND disabled_at IS NULL`, id)
		return err
	})
}
func (db *Database) SetFigmaBindingHealth(ctx context.Context, id, status, errorCode string) error {
	if !oneOf(status, "active", "needs_attention") {
		return ErrSpaceInvalid
	}
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE figma_space_bindings SET status=$2,last_error_code=$3,updated_at=NOW() WHERE id=$1 AND disabled_at IS NULL`, id, status, errorCode)
		return err
	})
}
func (db *Database) DisableFigmaBinding(ctx context.Context, userID, spaceID, id string) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionIntegrationsManage); err != nil {
			return err
		}
		var integrationID, resourceID string
		if err := tx.QueryRowContext(ctx, `UPDATE figma_space_bindings SET status='disabled',disabled_at=NOW(),updated_at=NOW() WHERE id=$1 AND space_id=$2 AND disabled_at IS NULL RETURNING integration_id,shared_resource_id`, id, spaceID).Scan(&integrationID, &resourceID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrSpaceNotFound
			}
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE space_integrations SET status='disabled',updated_at=NOW() WHERE id=$1`, integrationID)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE provider_shared_resources SET status='disabled',updated_at=NOW() WHERE id=$1`, resourceID)
		return err
	})
}

type FigmaWebhookSubscription struct {
	ID            string `json:"id"`
	BindingID     string `json:"binding_id"`
	WebhookID     string `json:"webhook_id"`
	EventType     string `json:"event_type"`
	Status        string `json:"status"`
	LastErrorCode string `json:"last_error_code,omitempty"`
}

func (db *Database) SaveFigmaWebhookSubscription(ctx context.Context, bindingID, webhookID, eventType, passcodeHash string) (*FigmaWebhookSubscription, error) {
	item := &FigmaWebhookSubscription{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `INSERT INTO figma_webhook_subscriptions(id,binding_id,webhook_id,event_type,passcode_hash) VALUES($1,$2,$3,$4,$5) ON CONFLICT(binding_id,event_type) DO UPDATE SET webhook_id=EXCLUDED.webhook_id,passcode_hash=EXCLUDED.passcode_hash,status='active',last_error_code='',updated_at=NOW() RETURNING id,binding_id,webhook_id,event_type,status,last_error_code`, `figma_hook_`+uuid.NewString(), bindingID, webhookID, eventType, passcodeHash).Scan(&item.ID, &item.BindingID, &item.WebhookID, &item.EventType, &item.Status, &item.LastErrorCode)
	})
	return item, err
}
func (db *Database) FigmaWebhookSubscriptions(ctx context.Context, bindingID string) ([]FigmaWebhookSubscription, error) {
	items := []FigmaWebhookSubscription{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT id,binding_id,webhook_id,event_type,status,last_error_code FROM figma_webhook_subscriptions WHERE binding_id=$1 AND status<>'disabled' ORDER BY event_type`, bindingID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item FigmaWebhookSubscription
			if err := rows.Scan(&item.ID, &item.BindingID, &item.WebhookID, &item.EventType, &item.Status, &item.LastErrorCode); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}
func (db *Database) DisableFigmaWebhookSubscriptions(ctx context.Context, bindingID string) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE figma_webhook_subscriptions SET status='disabled',updated_at=NOW() WHERE binding_id=$1 AND status<>'disabled'`, bindingID)
		return err
	})
}

func (db *Database) FigmaBindingsForConnection(ctx context.Context, userID, connectionID string) ([]FigmaBinding, error) {
	items := []FigmaBinding{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT `+figmaBindingColumns+` FROM figma_space_bindings WHERE connection_id=$1 AND bound_by_user_id=$2 AND disabled_at IS NULL`, connectionID, userID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item FigmaBinding
			if err := scanFigmaBinding(rows, &item); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}

func (db *Database) DisableFigmaBindingsForConnection(ctx context.Context, userID, connectionID string) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE figma_webhook_subscriptions SET status='disabled',updated_at=NOW() WHERE binding_id IN (SELECT id FROM figma_space_bindings WHERE connection_id=$1 AND bound_by_user_id=$2)`, connectionID, userID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE provider_shared_resources SET status='disabled',updated_at=NOW() WHERE id IN (SELECT shared_resource_id FROM figma_space_bindings WHERE connection_id=$1 AND bound_by_user_id=$2)`, connectionID, userID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE space_integrations SET status='disabled',updated_at=NOW() WHERE id IN (SELECT integration_id FROM figma_space_bindings WHERE connection_id=$1 AND bound_by_user_id=$2)`, connectionID, userID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE figma_space_bindings SET status='disabled',disabled_at=NOW(),updated_at=NOW() WHERE connection_id=$1 AND bound_by_user_id=$2 AND disabled_at IS NULL`, connectionID, userID)
		return err
	})
}
