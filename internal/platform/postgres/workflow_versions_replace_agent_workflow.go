package db

import (
	"context"
	"database/sql"
	"encoding/json"
)

func (db *Database) SpaceIntegrations(ctx context.Context, userID, spaceID string) ([]SpaceIntegration, error) {
	items := []SpaceIntegration{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT id,space_id,provider,display_name,'',granted_permissions,status,connected_by_user_id,created_at,updated_at
			FROM space_integrations WHERE space_id=$1 ORDER BY provider,display_name,id`, spaceID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item SpaceIntegration
			var permissionsRaw []byte
			if err := rows.Scan(&item.ID, &item.SpaceID, &item.Provider, &item.DisplayName, &item.CredentialReference, &permissionsRaw, &item.Status, &item.ConnectedByUserID, &item.CreatedAt, &item.UpdatedAt); err != nil {
				return err
			}
			_ = json.Unmarshal(permissionsRaw, &item.GrantedPermissions)
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}


func mustJSON(value any) json.RawMessage {
	raw, _ := json.Marshal(value)
	return raw
}
