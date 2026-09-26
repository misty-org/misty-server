package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/kannachi323/misty/server/internal/platform/transport"
)

type SettingsProfile struct {
	ID            string         `json:"id"`
	Name          string         `json:"name"`
	SchemaVersion int            `json:"schemaVersion"`
	Revision      int64          `json:"revision"`
	Values        map[string]any `json:"values"`
}
type SettingsProfilePatch struct {
	MutationID string         `json:"mutationId"`
	Name       *string        `json:"name,omitempty"`
	Set        map[string]any `json:"set"`
	Unset      []string       `json:"unset"`
}

var ErrSettingsProfileNotFound = errors.New("settings profile not found")
var ErrSettingsProfileMutation = errors.New("mutation ID was already used for different changes")

func (db *Database) SettingsProfiles(ctx context.Context, userID string) ([]SettingsProfile, error) {
	rows, err := db.Conn.QueryContext(ctx, `SELECT id,name,schema_version,revision,values_json FROM settings_profiles WHERE user_id=$1 AND NOT deleted ORDER BY name,id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	profiles := []SettingsProfile{}
	for rows.Next() {
		var p SettingsProfile
		var raw []byte
		if err = rows.Scan(&p.ID, &p.Name, &p.SchemaVersion, &p.Revision, &raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &p.Values); err != nil {
			return nil, err
		}
		profiles = append(profiles, p)
	}
	return profiles, rows.Err()
}
func validateProfileName(name string) error {
	if strings.TrimSpace(name) == "" || len([]rune(name)) > 80 {
		return fmt.Errorf("profile name must contain 1–80 characters")
	}
	return nil
}
func (db *Database) CreateSettingsProfile(ctx context.Context, userID, id, name string, values map[string]any) (SettingsProfile, error) {
	var p SettingsProfile
	if _, err := uuid.Parse(id); err != nil {
		return p, err
	}
	if err := validateProfileName(name); err != nil {
		return p, err
	}
	if err := transport.ValidateProfilePatch(values, nil); err != nil {
		return p, err
	}
	if values == nil {
		values = map[string]any{}
	}
	raw, _ := json.Marshal(values)
	tx, err := db.Conn.BeginTx(ctx, nil)
	if err != nil {
		return p, err
	}
	defer tx.Rollback()
	// Caller-generated IDs make creation retryable without duplicating profiles.
	_, err = tx.ExecContext(ctx, `INSERT INTO settings_profiles(id,user_id,name,values_json) VALUES($1,$2,$3,$4) ON CONFLICT(id) DO NOTHING`, id, userID, strings.TrimSpace(name), raw)
	if err != nil {
		return p, err
	}
	var deleted bool
	err = tx.QueryRowContext(ctx, `SELECT id,name,schema_version,revision,values_json,deleted FROM settings_profiles WHERE id=$1 AND user_id=$2 FOR UPDATE`, id, userID).Scan(&p.ID, &p.Name, &p.SchemaVersion, &p.Revision, &raw, &deleted)
	if errors.Is(err, sql.ErrNoRows) || deleted {
		return p, ErrSettingsProfileNotFound
	}
	if err != nil {
		return p, err
	}
	if err = json.Unmarshal(raw, &p.Values); err != nil {
		return p, err
	}
	if err = notifySettingsProfile(ctx, tx, userID, id); err != nil {
		return p, err
	}
	return p, tx.Commit()
}
func (db *Database) PatchSettingsProfile(ctx context.Context, userID, id string, patch SettingsProfilePatch) (SettingsProfile, error) {
	var p SettingsProfile
	if _, err := uuid.Parse(patch.MutationID); err != nil {
		return p, err
	}
	if err := transport.ValidateProfilePatch(patch.Set, patch.Unset); err != nil {
		return p, err
	}
	if patch.Name != nil {
		if err := validateProfileName(*patch.Name); err != nil {
			return p, err
		}
	}
	encoded, _ := json.Marshal(patch)
	digest := sha256.Sum256(encoded)
	hash := hex.EncodeToString(digest[:])
	tx, err := db.Conn.BeginTx(ctx, nil)
	if err != nil {
		return p, err
	}
	defer tx.Rollback()
	var raw []byte
	var deleted bool
	err = tx.QueryRowContext(ctx, `SELECT id,name,schema_version,revision,values_json,deleted FROM settings_profiles WHERE id=$1 AND user_id=$2 FOR UPDATE`, id, userID).Scan(&p.ID, &p.Name, &p.SchemaVersion, &p.Revision, &raw, &deleted)
	if errors.Is(err, sql.ErrNoRows) || deleted {
		return p, ErrSettingsProfileNotFound
	}
	if err != nil {
		return p, err
	}
	if err = json.Unmarshal(raw, &p.Values); err != nil {
		return p, err
	}
	var previous string
	err = tx.QueryRowContext(ctx, `SELECT payload_hash FROM settings_profile_receipts WHERE profile_id=$1 AND mutation_id=$2`, id, patch.MutationID).Scan(&previous)
	if err == nil {
		if previous != hash {
			return p, ErrSettingsProfileMutation
		}
		return p, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return p, err
	}
	for key, value := range patch.Set {
		p.Values[key] = value
	}
	for _, key := range patch.Unset {
		delete(p.Values, key)
	}
	if patch.Name != nil {
		p.Name = strings.TrimSpace(*patch.Name)
	}
	p.Revision++
	raw, _ = json.Marshal(p.Values)
	_, err = tx.ExecContext(ctx, `UPDATE settings_profiles SET name=$2,values_json=$3,revision=$4,updated_at=now() WHERE id=$1`, id, p.Name, raw, p.Revision)
	if err != nil {
		return p, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO settings_profile_receipts(profile_id,mutation_id,payload_hash) VALUES($1,$2,$3)`, id, patch.MutationID, hash)
	if err != nil {
		return p, err
	}
	if err = notifySettingsProfile(ctx, tx, userID, id); err != nil {
		return p, err
	}
	return p, tx.Commit()
}
func (db *Database) DeleteSettingsProfile(ctx context.Context, userID, id string) error {
	tx, err := db.Conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE settings_profiles SET deleted=true,revision=revision+1,updated_at=now() WHERE id=$1 AND user_id=$2 AND NOT deleted`, id, userID)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count > 0 {
		if err = notifySettingsProfile(ctx, tx, userID, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func notifySettingsProfile(ctx context.Context, tx *sql.Tx, userID, id string) error {
	payload, _ := json.Marshal(transport.AccountEvent{UserID: userID, Topic: "settings-profiles", ID: id})
	_, err := tx.ExecContext(ctx, `SELECT pg_notify('misty_account_events',$1)`, string(payload))
	return err
}
