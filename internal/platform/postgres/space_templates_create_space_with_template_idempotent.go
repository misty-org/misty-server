package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"github.com/google/uuid"
)

func (db *Database) CreateSpaceWithTemplateIdempotent(
	ctx context.Context,
	userID, name, templateID string,
	providers []string,
	idempotencyKey string,
) (*CreateSpaceResult, error) {
	name, err := normalizeSpaceName(name)
	if err != nil {
		return nil, err
	}
	template, ok := TestingTemplateByID(templateID)
	if !ok {
		return nil, ErrSpaceInvalid
	}
	providers, err = TestingNormalizeSetupProviders(providers)
	if err != nil {
		return nil, err
	}
	result := &CreateSpaceResult{
		Space: Space{
			ID: "space_" + uuid.NewString(), SecurityDomainID: "sd_" + uuid.NewString(),
			OwnerUserID: userID, Name: name, Kind: "standard", Role: "owner", MemberCount: 1,
		},
		Setup: SpaceSetup{SelectedProviders: providers, PendingProviders: append([]string(nil), providers...), CompletedProviders: []string{}},
	}
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if len(idempotencyKey) > 200 {
		return nil, ErrSpaceInvalid
	}
	fingerprintInput, _ := json.Marshal(struct {
		Name      string   `json:"name"`
		Template  string   `json:"template"`
		Providers []string `json:"providers"`
	}{Name: name, Template: template.ID, Providers: providers})
	fingerprintDigest := sha256.Sum256(fingerprintInput)
	fingerprint := hex.EncodeToString(fingerprintDigest[:])
	existingSpaceID := ""
	err = db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if idempotencyKey != "" {
			if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`,
				"spaces:create:"+userID+":"+idempotencyKey); err != nil {
				return err
			}
			var storedFingerprint string
			err := tx.QueryRowContext(ctx, `SELECT request_fingerprint,space_id
				FROM space_creation_requests WHERE user_id=$1 AND idempotency_key=$2`,
				userID, idempotencyKey).Scan(&storedFingerprint, &existingSpaceID)
			if err == nil {
				if storedFingerprint != fingerprint {
					return ErrSpaceConflict
				}
				return nil
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO security_domains(id,kind,owner_user_id,space_id) VALUES($1,'space',$2,$3)`, result.Space.SecurityDomainID, userID, result.Space.ID); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `INSERT INTO spaces(id,owner_user_id,name,security_domain_id) VALUES($1,$2,$3,$4) RETURNING created_at,updated_at`, result.Space.ID, userID, name, result.Space.SecurityDomainID).Scan(&result.Space.CreatedAt, &result.Space.UpdatedAt); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO space_storage_usage(space_id) VALUES($1)`, result.Space.ID); err != nil {
			return err
		}
		if err := addSpaceMembershipTx(ctx, tx, result.Space.ID, userID, "owner"); err != nil {
			return err
		}
		memberPermissions := `["space.view","messages.read","messages.write","attachments.upload","library.view","library.upload","library.add","library.edit","library.download","library.import","storage.view_own_usage","tasks.view","tasks.manage"]`
		if _, err := tx.ExecContext(ctx, `INSERT INTO space_roles(id,space_id,name,is_everyone,permissions) VALUES($1,$2,'@everyone',TRUE,$3::jsonb)`, "role_"+uuid.NewString(), result.Space.ID, memberPermissions); err != nil {
			return err
		}
		for _, provider := range providers {
			if _, err := tx.ExecContext(ctx, `INSERT INTO space_setup_integrations(space_id,provider) VALUES($1,$2)`, result.Space.ID, provider); err != nil {
				return err
			}
		}
		if err := seedSpaceTemplateTx(ctx, tx, result.Space.ID, userID, *template); err != nil {
			return err
		}
		if _, err := recordSpaceEventTx(ctx, tx, result.Space.ID, userID, "space.created", result.Space.ID, map[string]any{
			"name": name, "template_id": template.ID, "integration_providers": providers,
		}); err != nil {
			return err
		}
		if idempotencyKey != "" {
			_, err := tx.ExecContext(ctx, `INSERT INTO space_creation_requests
				(user_id,idempotency_key,request_fingerprint,space_id) VALUES($1,$2,$3,$4)`,
				userID, idempotencyKey, fingerprint, result.Space.ID)
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if existingSpaceID != "" {
		space, err := db.SpaceByID(ctx, userID, existingSpaceID)
		if err != nil {
			return nil, err
		}
		setup, err := db.SpaceSetup(ctx, userID, existingSpaceID)
		if err != nil {
			return nil, err
		}
		return &CreateSpaceResult{Space: *space, Setup: *setup}, nil
	}
	return result, nil
}

func seedSpaceTemplateTx(ctx context.Context, tx *sql.Tx, spaceID, userID string, template templateDefinition) error {
	if len(template.Tasks) > 0 {
		if _, err := tx.ExecContext(ctx, `INSERT INTO space_task_counters(space_id,last_number) VALUES($1,$2)`, spaceID, len(template.Tasks)); err != nil {
			return err
		}
		for index, title := range template.Tasks {
			number := index + 1
			if _, err := tx.ExecContext(ctx, `INSERT INTO space_tasks
				(id,space_id,task_number,task_key,title,status,priority,rank,due_timezone,source_refs,created_by_user_id)
				VALUES($1,$2,$3,$4,$5,'todo','medium',$6,'UTC','[]'::jsonb,$7)`,
				"task_"+uuid.NewString(), spaceID, number, "MST-"+jsonNumber(number), title, int64(number*1024), userID); err != nil {
				return err
			}
		}
	}
	for _, name := range template.Collections {
		if _, err := tx.ExecContext(ctx, `INSERT INTO space_albums(id,space_id,name,description,created_by_user_id)
			VALUES($1,$2,$3,'',$4)`, "album_"+uuid.NewString(), spaceID, name, userID); err != nil {
			return err
		}
	}
	if template.NoteTitle == "" {
		return nil
	}
	noteID := "note_" + uuid.NewString()
	plain := strings.TrimSpace(strings.ReplaceAll(template.NoteMarkdown, "#", ""))
	if _, err := tx.ExecContext(ctx, `INSERT INTO space_notes
		(id,space_id,creator_user_id,title_projection,plain_text_projection)
		VALUES($1,$2,$3,$4,$5)`, noteID, spaceID, userID, template.NoteTitle, plain); err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]any{"markdown": template.NoteMarkdown, "title": template.NoteTitle})
	if _, err := tx.ExecContext(ctx, `INSERT INTO space_note_control_outbox(id,note_id,command,payload)
		VALUES($1,$2,'bootstrap',$3)`, "notectl_"+uuid.NewString(), noteID, payload); err != nil {
		return err
	}
	return recordNoteEventTx(ctx, tx, spaceID, userID, "note.created", noteID, nil)
}

func jsonNumber(value int) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}

func (db *Database) SpaceSetup(ctx context.Context, userID, spaceID string) (*SpaceSetup, error) {
	setup := &SpaceSetup{SelectedProviders: []string{}, CompletedProviders: []string{}, PendingProviders: []string{}}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT provider,status FROM space_setup_integrations WHERE space_id=$1 ORDER BY provider`, spaceID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var provider, status string
			if err := rows.Scan(&provider, &status); err != nil {
				return err
			}
			setup.SelectedProviders = append(setup.SelectedProviders, provider)
			if status == "configured" || status == "skipped" {
				setup.CompletedProviders = append(setup.CompletedProviders, provider)
			} else {
				setup.PendingProviders = append(setup.PendingProviders, provider)
			}
		}
		return rows.Err()
	})
	return setup, err
}

func (db *Database) SetSpaceSetupProviderStatus(ctx context.Context, userID, spaceID, provider, status string) error {
	if _, err := TestingNormalizeSetupProviders([]string{provider}); err != nil {
		return err
	}
	if status != "selected" && status != "authorized" && status != "configured" && status != "skipped" {
		return ErrSpaceInvalid
	}
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpaceOwnerTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE space_setup_integrations SET status=$1,updated_at=NOW()
			WHERE space_id=$2 AND provider=$3`, status, spaceID, provider)
		if err != nil {
			return err
		}
		if affected, _ := result.RowsAffected(); affected == 0 {
			return ErrSpaceNotFound
		}
		return nil
	})
}
