package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"strings"
	"time"
)

type SpaceTemplateApp struct {
	AppID           string          `json:"app_id"`
	ReleaseMetadata json.RawMessage `json:"release_metadata"`
}
type PersonalSpaceTemplate struct {
	ID          string             `json:"id"`
	Name        string             `json:"name"`
	Description string             `json:"description"`
	Apps        []SpaceTemplateApp `json:"apps"`
	Version     int                `json:"version"`
	CreatedAt   time.Time          `json:"created_at"`
	UpdatedAt   time.Time          `json:"updated_at"`
}

func scanPersonalSpaceTemplate(row rowScanner) (PersonalSpaceTemplate, error) {
	var item PersonalSpaceTemplate
	var raw []byte
	err := row.Scan(&item.ID, &item.Name, &item.Description, &raw, &item.Version, &item.CreatedAt, &item.UpdatedAt)
	if err == nil {
		err = json.Unmarshal(raw, &item.Apps)
	}
	return item, err
}

const personalTemplateColumns = `id,name,description,apps,version,created_at,updated_at`

func (db *Database) PersonalSpaceTemplates(ctx context.Context, userID string) ([]PersonalSpaceTemplate, error) {
	items := []PersonalSpaceTemplate{}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT `+personalTemplateColumns+` FROM personal_space_templates WHERE user_id=$1 ORDER BY updated_at DESC,id`, userID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			item, err := scanPersonalSpaceTemplate(rows)
			if err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}
func (db *Database) SavePersonalSpaceTemplate(ctx context.Context, userID, id, spaceID, name, description string) (*PersonalSpaceTemplate, error) {
	name, description = strings.TrimSpace(name), strings.TrimSpace(description)
	if name == "" || len([]rune(name)) > 80 || len([]rune(description)) > 1000 {
		return nil, ErrSpaceInvalid
	}
	create := id == ""
	if create {
		id = "template_" + uuid.NewString()
		if spaceID == "" {
			return nil, ErrSpaceInvalid
		}
	}
	var item PersonalSpaceTemplate
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if !create {
			var owner string
			if err := tx.QueryRowContext(ctx, `SELECT user_id FROM personal_space_templates WHERE id=$1 FOR UPDATE`, id).Scan(&owner); err != nil || owner != userID {
				return ErrSpaceNotFound
			}
		}
		var raw []byte
		if spaceID != "" {
			if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionAppsManage); err != nil {
				return err
			}
			if err := lockSpaceApps(ctx, tx, spaceID); err != nil {
				return err
			}
			rows, err := tx.QueryContext(ctx, `SELECT app_id,release_metadata FROM space_app_installations WHERE space_id=$1 AND state='installed' ORDER BY pin_rank,app_id`, spaceID)
			if err != nil {
				return err
			}
			apps := []SpaceTemplateApp{}
			for rows.Next() {
				var app SpaceTemplateApp
				if err := rows.Scan(&app.AppID, &app.ReleaseMetadata); err != nil {
					rows.Close()
					return err
				}
				apps = append(apps, app)
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return err
			}
			raw, err = json.Marshal(apps)
			if err != nil {
				return err
			}
		}
		var err error
		if create {
			item, err = scanPersonalSpaceTemplate(tx.QueryRowContext(ctx, `INSERT INTO personal_space_templates(id,user_id,name,description,apps) VALUES($1,$2,$3,$4,$5) RETURNING `+personalTemplateColumns, id, userID, name, description, raw))
		} else {
			item, err = scanPersonalSpaceTemplate(tx.QueryRowContext(ctx, `UPDATE personal_space_templates SET name=$3,description=$4,apps=COALESCE($5::jsonb,apps),version=version+1,updated_at=NOW() WHERE id=$1 AND user_id=$2 RETURNING `+personalTemplateColumns, id, userID, name, description, raw))
		}
		return err
	})
	return &item, err
}
func (db *Database) DeletePersonalSpaceTemplate(ctx context.Context, userID, id string) error {
	return db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `DELETE FROM personal_space_templates WHERE id=$1 AND user_id=$2`, id, userID)
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if count == 0 {
			return ErrSpaceNotFound
		}
		return nil
	})
}
func (db *Database) personalTemplateDefinition(ctx context.Context, userID, id string) (*templateDefinition, error) {
	var item PersonalSpaceTemplate
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		var err error
		item, err = scanPersonalSpaceTemplate(tx.QueryRowContext(ctx, `SELECT `+personalTemplateColumns+` FROM personal_space_templates WHERE id=$1 AND user_id=$2`, id, userID))
		return err
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceNotFound
	}
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for _, app := range item.Apps {
		ids = append(ids, app.AppID)
	}
	return &templateDefinition{SpaceTemplate: SpaceTemplate{ID: item.ID, Name: item.Name, Description: item.Description, Version: item.Version, AppIDs: ids}}, nil
}
