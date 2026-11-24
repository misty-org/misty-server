package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/lib/pq"
	"github.com/misty-org/misty-server/internal/spaces"
)

var ErrSpaceIntegrationLimit = errors.New("space integration limit reached")

type SpaceIntegration struct {
	ID         string          `json:"id"`
	SpaceID    string          `json:"-"`
	Name       string          `json:"name"`
	Avatar     json.RawMessage `json:"avatar"`
	Access     []string        `json:"access"`
	KeyPrefix  string          `json:"key_prefix"`
	CreatedBy  string          `json:"created_by"`
	CreatedAt  time.Time       `json:"created_at"`
	LastUsedAt *time.Time      `json:"last_used_at"`
}

type SpaceMemberIntegration struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Avatar    json.RawMessage `json:"avatar"`
	Access    []string        `json:"access"`
	KeyPrefix string          `json:"key_prefix"`
}

type SpaceIntegrationInput struct {
	Name      string
	Avatar    json.RawMessage
	Access    []string
	KeyPrefix string
}

type SpaceIntegrationPatch struct {
	Name      *string
	Avatar    json.RawMessage
	Access    []string
	SetAccess bool
	KeyPrefix *string
}

const spaceIntegrationColumns = `i.id,i.space_id,i.name,i.avatar,i.access,i.key_prefix,i.created_by,i.created_at,i.last_used_at`

func scanSpaceIntegration(row interface{ Scan(...any) error }, out *SpaceIntegration) error {
	var avatar []byte
	var lastUsed sql.NullTime
	if err := row.Scan(&out.ID, &out.SpaceID, &out.Name, &avatar, pq.Array(&out.Access), &out.KeyPrefix, &out.CreatedBy,
		&out.CreatedAt, &lastUsed); err != nil {
		return err
	}
	out.Avatar = json.RawMessage(avatar)
	if len(out.Avatar) == 0 {
		out.Avatar = json.RawMessage(`{}`)
	}
	if out.Access == nil {
		out.Access = []string{}
	}
	out.LastUsedAt = nil
	if lastUsed.Valid {
		at := lastUsed.Time
		out.LastUsedAt = &at
	}
	return nil
}

func requireIntegrationManagerTx(ctx context.Context, tx *sql.Tx, spaceID, userID string) error {
	if err := requireSpaceAuthorityTx(ctx, tx, spaceID, userID, spaces.PermissionIntegrationsManage); err != nil {
		return err
	}
	return requireShareableSpaceTx(ctx, tx, spaceID)
}

func listSpaceIntegrationsTx(ctx context.Context, tx *sql.Tx, spaceID string) ([]SpaceIntegration, error) {
	rows, err := tx.QueryContext(ctx, `SELECT `+spaceIntegrationColumns+` FROM space_integrations i
		WHERE i.space_id=$1 ORDER BY i.created_at,i.id`, spaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SpaceIntegration{}
	for rows.Next() {
		var integration SpaceIntegration
		if err := scanSpaceIntegration(rows, &integration); err != nil {
			return nil, err
		}
		out = append(out, integration)
	}
	return out, rows.Err()
}

func (db *Database) SpaceIntegrations(ctx context.Context, userID, spaceID string) ([]SpaceIntegration, error) {
	var out []SpaceIntegration
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireIntegrationManagerTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		var err error
		out, err = listSpaceIntegrationsTx(ctx, tx, spaceID)
		return err
	})
	return out, err
}

func (db *Database) SpaceMemberIntegrations(ctx context.Context, userID, spaceID string) ([]SpaceMemberIntegration, error) {
	out := []SpaceMemberIntegration{}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		integrations, err := listSpaceIntegrationsTx(ctx, tx, spaceID)
		for _, integration := range integrations {
			out = append(out, SpaceMemberIntegration{ID: integration.ID, Name: integration.Name, Avatar: integration.Avatar,
				Access: integration.Access, KeyPrefix: integration.KeyPrefix})
		}
		return err
	})
	return out, err
}

func normalizeSpaceIntegrationInput(input SpaceIntegrationInput) (SpaceIntegrationInput, error) {
	var err error
	if input.Name, err = spaces.NormalizeIntegrationName(input.Name); err != nil {
		return input, ErrSpaceInvalid
	}
	if input.Avatar, err = spaces.NormalizeIntegrationAvatar(input.Avatar); err != nil {
		return input, ErrSpaceInvalid
	}
	if input.Access, err = spaces.NormalizeIntegrationAccess(input.Access); err != nil {
		return input, ErrSpaceInvalid
	}
	if input.KeyPrefix, err = spaces.NormalizeIntegrationKeyPrefix(input.KeyPrefix); err != nil {
		return input, ErrSpaceInvalid
	}
	return input, nil
}

func requireSpaceIntegrationRoomTx(ctx context.Context, tx *sql.Tx, spaceID string) error {
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "space-integrations:"+spaceID); err != nil {
		return err
	}
	var ownerID string
	if err := tx.QueryRowContext(ctx, `SELECT owner_user_id FROM spaces WHERE id=$1`, spaceID).Scan(&ownerID); err != nil {
		return err
	}
	limits, _, err := accountControlsTx(ctx, tx, ownerID)
	if err != nil || limits.IntegrationsPerSpace == nil {
		return err
	}
	var count int64
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM space_integrations WHERE space_id=$1`, spaceID).Scan(&count); err != nil {
		return err
	}
	if count >= *limits.IntegrationsPerSpace {
		return ErrSpaceIntegrationLimit
	}
	return nil
}

func (db *Database) CreateSpaceIntegration(ctx context.Context, userID, spaceID string, input SpaceIntegrationInput, tokenHash string) (*SpaceIntegration, error) {
	input, err := normalizeSpaceIntegrationInput(input)
	if err != nil {
		return nil, err
	}
	id, err := spaces.NewIntegrationID()
	if err != nil {
		return nil, err
	}
	out := &SpaceIntegration{}
	err = db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireIntegrationManagerTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		if err := requireSpaceIntegrationRoomTx(ctx, tx, spaceID); err != nil {
			return err
		}
		if err := scanSpaceIntegration(tx.QueryRowContext(ctx, `INSERT INTO space_integrations AS i
			(id,space_id,name,avatar,created_by,access,key_prefix,token_hash)
			VALUES($1,$2,$3,$4::jsonb,$5,$6,$7,$8) RETURNING `+spaceIntegrationColumns,
			id, spaceID, input.Name, string(input.Avatar), userID, pq.Array(input.Access), input.KeyPrefix, tokenHash), out); err != nil {
			return err
		}
		_, err := recordSpaceEventTx(ctx, tx, spaceID, userID, "member.integration_added", id, map[string]any{"integration_id": id})
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (db *Database) UpdateSpaceIntegration(ctx context.Context, userID, spaceID, integrationID string, patch SpaceIntegrationPatch) (*SpaceIntegration, error) {
	out := &SpaceIntegration{}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireIntegrationManagerTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		current := SpaceIntegration{}
		err := scanSpaceIntegration(tx.QueryRowContext(ctx, `SELECT `+spaceIntegrationColumns+` FROM space_integrations i
			WHERE i.space_id=$1 AND i.id=$2 FOR UPDATE`, spaceID, integrationID), &current)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrSpaceNotFound
		}
		if err != nil {
			return err
		}
		input := SpaceIntegrationInput{Name: current.Name, Avatar: current.Avatar, Access: current.Access, KeyPrefix: current.KeyPrefix}
		if patch.Name != nil {
			input.Name = *patch.Name
		}
		if patch.Avatar != nil {
			input.Avatar = patch.Avatar
		}
		if patch.SetAccess {
			input.Access = patch.Access
		}
		if patch.KeyPrefix != nil {
			input.KeyPrefix = *patch.KeyPrefix
		}
		if input, err = normalizeSpaceIntegrationInput(input); err != nil {
			return err
		}
		if err := scanSpaceIntegration(tx.QueryRowContext(ctx, `UPDATE space_integrations AS i
			SET name=$3,avatar=$4::jsonb,access=$5,key_prefix=$6
			WHERE i.space_id=$1 AND i.id=$2 RETURNING `+spaceIntegrationColumns,
			spaceID, integrationID, input.Name, string(input.Avatar), pq.Array(input.Access), input.KeyPrefix), out); err != nil {
			return err
		}
		if _, err = recordSpaceEventTx(ctx, tx, spaceID, userID, "member.integration_updated", integrationID,
			map[string]any{"integration_id": integrationID}); err != nil {
			return err
		}
		if integrationRightsChanged(current, *out) {
			return signalCollabIntegrationChangedTx(ctx, tx, integrationID)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (db *Database) ReplaceSpaceIntegrationToken(ctx context.Context, userID, spaceID, integrationID, tokenHash string) error {
	return db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireIntegrationManagerTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE space_integrations SET token_hash=$3 WHERE space_id=$1 AND id=$2`,
			spaceID, integrationID, tokenHash)
		if err != nil {
			return err
		}
		if changed, _ := result.RowsAffected(); changed == 0 {
			return ErrSpaceNotFound
		}
		return signalCollabIntegrationEndedTx(ctx, tx, integrationID)
	})
}

func (db *Database) RemoveSpaceIntegration(ctx context.Context, userID, spaceID, integrationID string) error {
	return db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireIntegrationManagerTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `DELETE FROM space_integrations WHERE space_id=$1 AND id=$2`, spaceID, integrationID)
		if err != nil {
			return err
		}
		if removed, _ := result.RowsAffected(); removed == 0 {
			return ErrSpaceNotFound
		}
		if err := signalCollabIntegrationEndedTx(ctx, tx, integrationID); err != nil {
			return err
		}
		_, err = recordSpaceEventTx(ctx, tx, spaceID, userID, "member.integration_removed", integrationID,
			map[string]any{"integration_id": integrationID})
		return err
	})
}

func (db *Database) SpaceIntegrationByToken(ctx context.Context, tokenHash string) (*SpaceIntegration, error) {
	out := &SpaceIntegration{}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		err := scanSpaceIntegration(tx.QueryRowContext(ctx, `UPDATE space_integrations AS i SET last_used_at=now()
			FROM spaces s
			WHERE i.token_hash=$1 AND s.id=i.space_id AND s.lifecycle_state='active' AND s.kind='space' AND NOT s.is_default
			RETURNING `+spaceIntegrationColumns, tokenHash), out)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrSpaceNotFound
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func integrationRightsChanged(before, after SpaceIntegration) bool {
	if before.KeyPrefix != after.KeyPrefix || before.Name != after.Name || len(before.Access) != len(after.Access) {
		return true
	}
	held := make(map[string]bool, len(before.Access))
	for _, permission := range before.Access {
		held[permission] = true
	}
	for _, permission := range after.Access {
		if !held[permission] {
			return true
		}
	}
	return false
}
