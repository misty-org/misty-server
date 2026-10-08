package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

// PersonalSpaceTemplate is a template someone saved from one of their Spaces.
// It is served in the same shape as a built-in template.
type PersonalSpaceTemplate struct {
	SpaceTemplate
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// storedTemplate is the personal_space_templates.definition column.
type storedTemplate struct {
	Tasks       []string       `json:"tasks"`
	Notes       []templateNote `json:"notes"`
	Collections []string       `json:"collections"`
}

const personalTemplateColumns = `id,name,description,definition,version,created_at,updated_at`

func scanPersonalSpaceTemplate(row rowScanner) (PersonalSpaceTemplate, templateDefinition, error) {
	var item PersonalSpaceTemplate
	var raw []byte
	var stored storedTemplate
	err := row.Scan(&item.ID, &item.Name, &item.Description, &raw, &item.Version, &item.CreatedAt, &item.UpdatedAt)
	if err == nil {
		err = json.Unmarshal(raw, &stored)
	}
	if err != nil {
		return item, templateDefinition{}, err
	}
	definition := templateDefinition{
		SpaceTemplate: SpaceTemplate{
			ID: item.ID, Name: item.Name, Description: item.Description, Version: item.Version,
			Icon: "bookmark", SuggestedName: item.Name, Personal: true,
		},
		Tasks: stored.Tasks, Notes: stored.Notes, Collections: stored.Collections,
	}
	definition.finish()
	item.SpaceTemplate = definition.SpaceTemplate
	return item, definition, nil
}

func (db *Database) PersonalSpaceTemplates(ctx context.Context, userID string) ([]PersonalSpaceTemplate, error) {
	items := []PersonalSpaceTemplate{}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT `+personalTemplateColumns+` FROM personal_space_templates WHERE user_id=$1 ORDER BY updated_at DESC,id`, userID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			item, _, err := scanPersonalSpaceTemplate(rows)
			if err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}

// SavePersonalSpaceTemplate creates a template from spaceID, or renames an
// existing one. Giving spaceID on an update captures that Space again.
func (db *Database) SavePersonalSpaceTemplate(ctx context.Context, userID, id, spaceID, name, description string, parts TemplateParts) (*PersonalSpaceTemplate, error) {
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
		// Nil keeps the stored definition on an update.
		var definition any
		if spaceID != "" {
			captured, err := captureSpaceStructureTx(ctx, tx, userID, spaceID, parts)
			if err != nil {
				return err
			}
			raw, _ := json.Marshal(storedTemplate{Tasks: captured.Tasks, Notes: captured.Notes, Collections: captured.Collections})
			definition = string(raw)
		}
		var err error
		if create {
			item, _, err = scanPersonalSpaceTemplate(tx.QueryRowContext(ctx, `INSERT INTO personal_space_templates(id,user_id,name,description,definition) VALUES($1,$2,$3,$4,$5::jsonb) RETURNING `+personalTemplateColumns, id, userID, name, description, definition))
		} else {
			item, _, err = scanPersonalSpaceTemplate(tx.QueryRowContext(ctx, `UPDATE personal_space_templates SET name=$3,description=$4,definition=COALESCE($5::jsonb,definition),version=version+1,updated_at=NOW() WHERE id=$1 AND user_id=$2 RETURNING `+personalTemplateColumns, id, userID, name, description, definition))
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
	var definition templateDefinition
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		var err error
		_, definition, err = scanPersonalSpaceTemplate(tx.QueryRowContext(ctx, `SELECT `+personalTemplateColumns+` FROM personal_space_templates WHERE id=$1 AND user_id=$2`, id, userID))
		return err
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceNotFound
	}
	if err != nil {
		return nil, err
	}
	return &definition, nil
}
