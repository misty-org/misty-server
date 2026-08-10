package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

type WorkflowField struct {
	Name        string          `json:"name"`
	Type        string          `json:"type"`
	Description string          `json:"description,omitempty"`
	Required    bool            `json:"required,omitempty"`
	Schema      json.RawMessage `json:"schema,omitempty"`
}

type WorkflowCapability struct {
	ID                   string          `json:"id"`
	Name                 string          `json:"name"`
	Description          string          `json:"description"`
	Inputs               []WorkflowField `json:"inputs"`
	Outputs              []WorkflowField `json:"outputs"`
	ReadOnly             bool            `json:"readOnly"`
	Destructive          bool            `json:"destructive"`
	ConfirmationRequired bool            `json:"confirmationRequired"`
	Tags                 []string        `json:"tags"`
}

type WorkflowRuntime struct {
	Kind          string `json:"kind"`
	Compatibility string `json:"compatibility"`
}

type WorkflowMetadata struct {
	Capabilities         []WorkflowCapability `json:"capabilities"`
	RequiredIntegrations []string             `json:"requiredIntegrations"`
	RequiredPermissions  []string             `json:"requiredPermissions"`
	Runtime              WorkflowRuntime      `json:"runtime"`
	Tags                 []string             `json:"tags"`
}

type SuggestedAgentPreset struct {
	Name         string `json:"name"`
	Icon         string `json:"icon"`
	Description  string `json:"description"`
	Instructions string `json:"instructions"`
}

type WorkflowVersion struct {
	ID               string           `json:"id"`
	WorkflowID       string           `json:"workflow_id"`
	SpaceID          string           `json:"space_id"`
	StableIdentifier string           `json:"stable_identifier"`
	Version          string           `json:"version"`
	Name             string           `json:"name"`
	Description      string           `json:"description"`
	AuthorName       string           `json:"author_name"`
	Metadata         WorkflowMetadata `json:"metadata"`
	Definition       json.RawMessage  `json:"definition"`
	ChecksumSHA256   string           `json:"checksum_sha256"`
	CreatedByUserID  string           `json:"created_by_user_id"`
	CreatedAt        time.Time        `json:"created_at"`
}

type SpaceIntegration struct {
	ID                  string    `json:"id"`
	SpaceID             string    `json:"space_id"`
	Provider            string    `json:"provider"`
	DisplayName         string    `json:"display_name"`
	CredentialReference string    `json:"-"`
	GrantedPermissions  []string  `json:"granted_permissions"`
	Status              string    `json:"status"`
	ConnectedByUserID   string    `json:"connected_by_user_id"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

const workflowVersionColumns = `v.id,v.workflow_id,v.space_id,v.stable_identifier,v.version,v.name,v.description,v.author_name,v.metadata,v.definition,v.checksum_sha256,v.created_by_user_id,v.created_at`

const workflowVersionReturningColumns = `id,workflow_id,space_id,stable_identifier,version,name,description,author_name,metadata,definition,checksum_sha256,created_by_user_id,created_at`

func ValidateWorkflowMetadata(metadata WorkflowMetadata) error {
	if len(metadata.Capabilities) == 0 || len(metadata.Capabilities) > 100 {
		return ErrSpaceInvalid
	}
	seen := map[string]bool{}
	for _, capability := range metadata.Capabilities {
		if !validWorkflowToken(capability.ID, 100) || strings.TrimSpace(capability.Name) == "" || len([]rune(capability.Name)) > 160 || seen[capability.ID] {
			return ErrSpaceInvalid
		}
		seen[capability.ID] = true
		if capability.Destructive && !capability.ConfirmationRequired {
			return ErrSpaceInvalid
		}
		if err := validateWorkflowFields(capability.Inputs); err != nil {
			return err
		}
		if err := validateWorkflowFields(capability.Outputs); err != nil {
			return err
		}
	}
	if !validWorkflowToken(metadata.Runtime.Kind, 100) || strings.TrimSpace(metadata.Runtime.Compatibility) == "" {
		return ErrSpaceInvalid
	}
	for _, permission := range metadata.RequiredPermissions {
		if !validWorkflowToken(permission, 120) || !knownWorkflowPermission(permission) {
			return ErrSpaceInvalid
		}
	}
	for _, integration := range metadata.RequiredIntegrations {
		if !validWorkflowToken(integration, 120) {
			return ErrSpaceInvalid
		}
	}
	return nil
}

func knownWorkflowPermission(permission string) bool {
	if permission == "files.read" || permission == "files.write" {
		return true
	}
	for _, candidate := range configurableSpacePermissions {
		if permission == candidate {
			return true
		}
	}
	return false
}

func validateWorkflowFields(fields []WorkflowField) error {
	if len(fields) > 100 {
		return ErrSpaceInvalid
	}
	seen := map[string]bool{}
	for _, field := range fields {
		if !validWorkflowToken(field.Name, 100) || !validWorkflowToken(field.Type, 60) || seen[field.Name] {
			return ErrSpaceInvalid
		}
		seen[field.Name] = true
		if len(field.Schema) > 0 {
			var value any
			if json.Unmarshal(field.Schema, &value) != nil {
				return ErrSpaceInvalid
			}
		}
	}
	return nil
}

func validWorkflowToken(value string, maximum int) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > maximum {
		return false
	}
	for _, character := range value {
		if !(character == '-' || character == '_' || character == '.' || character >= '0' && character <= '9' || character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z') {
			return false
		}
	}
	return true
}

func (db *Database) WorkflowVersions(ctx context.Context, userID, spaceID, workflowID string) ([]WorkflowVersion, error) {
	items := []WorkflowVersion{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionStudioView); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT `+workflowVersionColumns+` FROM space_workflow_versions v WHERE v.space_id=$1 AND ($2='' OR v.workflow_id=$2) ORDER BY v.created_at DESC`, spaceID, workflowID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item WorkflowVersion
			if err := scanWorkflowVersion(rows, &item); err != nil {
				return err
			}
			if !TestingWorkflowChecksumValid(&item) {
				return ErrSpaceInvalid
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}

func (db *Database) WorkflowVersion(ctx context.Context, userID, spaceID, versionID string) (*WorkflowVersion, error) {
	out := &WorkflowVersion{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionStudioView); err != nil {
			return err
		}
		if err := scanWorkflowVersion(tx.QueryRowContext(ctx, `SELECT `+workflowVersionColumns+` FROM space_workflow_versions v WHERE v.id=$1 AND v.space_id=$2`, versionID, spaceID), out); err != nil {
			return err
		}
		if !TestingWorkflowChecksumValid(out) {
			return ErrSpaceInvalid
		}
		return nil
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceNotFound
	}
	return out, err
}

func (db *Database) CreateWorkflowVersion(ctx context.Context, userID, spaceID, workflowID, version string, metadata WorkflowMetadata, definition json.RawMessage) (*WorkflowVersion, error) {
	version = strings.TrimSpace(version)
	if !validWorkflowToken(version, 64) || ValidateWorkflowMetadata(metadata) != nil || !validJSONObject(definition) {
		return nil, ErrSpaceInvalid
	}
	metadataRaw, _ := json.Marshal(metadata)
	var definitionValue any
	if json.Unmarshal(definition, &definitionValue) != nil {
		return nil, ErrSpaceInvalid
	}
	canonicalDefinition, _ := json.Marshal(definitionValue)
	digest := sha256.Sum256(append(append([]byte{}, metadataRaw...), canonicalDefinition...))
	checksum := hex.EncodeToString(digest[:])
	out := &WorkflowVersion{ID: "wfver_" + uuid.NewString(), SpaceID: spaceID, WorkflowID: workflowID, Version: version, Metadata: metadata, Definition: canonicalDefinition, ChecksumSHA256: checksum, CreatedByUserID: userID}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionStudioManage); err != nil {
			return err
		}
		if err := validateWorkflowV2Tx(ctx, tx, spaceID, canonicalDefinition); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `SELECT stable_identifier,name,description,author_name FROM space_workflows WHERE id=$1 AND space_id=$2 AND creator_user_id=$3`, workflowID, spaceID, userID).Scan(&out.StableIdentifier, &out.Name, &out.Description, &out.AuthorName); err != nil {
			return err
		}
		err := scanWorkflowVersion(tx.QueryRowContext(ctx, `INSERT INTO space_workflow_versions(id,workflow_id,space_id,stable_identifier,version,name,description,author_name,metadata,definition,checksum_sha256,created_by_user_id)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) RETURNING `+workflowVersionReturningColumns,
			out.ID, workflowID, spaceID, out.StableIdentifier, version, out.Name, out.Description, out.AuthorName, metadataRaw, canonicalDefinition, checksum, userID), out)
		if err == nil {
			_, err = tx.ExecContext(ctx, `UPDATE space_workflows SET definition=$1,version=version+1,updated_at=NOW() WHERE id=$2`, canonicalDefinition, workflowID)
		}
		return err
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceNotFound
	}
	return out, err
}
