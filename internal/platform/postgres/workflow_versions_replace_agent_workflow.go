package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

func (db *Database) ReplaceAgentWorkflow(ctx context.Context, userID, spaceID, agentID, versionID string) (*SpaceStudioResource, error) {
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionStudioManage); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE space_agents a SET active_workflow_version_id=$1,updated_by_user_id=$2,version=version+1,updated_at=NOW()
			WHERE a.id=$3 AND a.space_id=$4 AND EXISTS(SELECT 1 FROM space_workflow_versions v WHERE v.id=$1 AND v.space_id=a.space_id)`, versionID, userID, agentID, spaceID)
		if err != nil {
			return err
		}
		if changed, _ := result.RowsAffected(); changed == 0 {
			return ErrSpaceNotFound
		}
		_, err = recordSpaceEventTx(ctx, tx, spaceID, userID, "agent.workflow.replaced", agentID, map[string]string{"workflow_version_id": versionID})
		return err
	})
	if err != nil {
		return nil, err
	}
	return db.SpaceStudioResourceByID(ctx, userID, spaceID, "agent", agentID)
}

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

func (db *Database) SaveSpaceIntegration(ctx context.Context, userID string, item SpaceIntegration) (*SpaceIntegration, error) {
	item.Provider, item.DisplayName, item.CredentialReference = strings.TrimSpace(item.Provider), strings.TrimSpace(item.DisplayName), strings.TrimSpace(item.CredentialReference)
	if !validWorkflowToken(item.Provider, 120) || item.DisplayName == "" || len([]rune(item.DisplayName)) > 120 || item.CredentialReference == "" || len(item.CredentialReference) > 500 {
		return nil, ErrSpaceInvalid
	}
	if item.Status == "" {
		item.Status = "active"
	}
	if item.GrantedPermissions == nil {
		item.GrantedPermissions = []string{}
	}
	if item.Status != "active" && item.Status != "needs_attention" && item.Status != "disabled" {
		return nil, ErrSpaceInvalid
	}
	for _, permission := range item.GrantedPermissions {
		if !validWorkflowToken(permission, 120) {
			return nil, ErrSpaceInvalid
		}
	}
	if item.ID == "" {
		item.ID = "integration_" + uuid.NewString()
	}
	permissions := mustJSON(item.GrantedPermissions)
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, item.SpaceID, PermissionAgentsRun); err != nil {
			return err
		}
		return tx.QueryRowContext(ctx, `INSERT INTO space_integrations(id,space_id,provider,display_name,credential_reference,granted_permissions,status,connected_by_user_id)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8)
			ON CONFLICT(id) DO UPDATE SET display_name=EXCLUDED.display_name,credential_reference=EXCLUDED.credential_reference,granted_permissions=EXCLUDED.granted_permissions,status=EXCLUDED.status,updated_at=NOW()
			WHERE space_integrations.space_id=EXCLUDED.space_id AND space_integrations.connected_by_user_id=EXCLUDED.connected_by_user_id
			RETURNING connected_by_user_id,created_at,updated_at`, item.ID, item.SpaceID, item.Provider, item.DisplayName, item.CredentialReference, permissions, item.Status, userID).Scan(&item.ConnectedByUserID, &item.CreatedAt, &item.UpdatedAt)
	})
	if err != nil {
		return nil, err
	}
	item.CredentialReference = ""
	return &item, nil
}

func loadWorkflowVersionTx(ctx context.Context, tx *sql.Tx, versionID string) (*WorkflowVersion, error) {
	if versionID == "" {
		return nil, ErrSpaceNotFound
	}
	out := &WorkflowVersion{}
	if err := scanWorkflowVersion(tx.QueryRowContext(ctx, `SELECT `+workflowVersionColumns+` FROM space_workflow_versions v WHERE v.id=$1`, versionID), out); err != nil {
		return nil, err
	}
	if !TestingWorkflowChecksumValid(out) {
		return nil, ErrSpaceInvalid
	}
	return out, nil
}

func TestingWorkflowChecksumValid(version *WorkflowVersion) bool {
	if version == nil {
		return false
	}
	metadataRaw, err := json.Marshal(version.Metadata)
	if err != nil {
		return false
	}
	var definition any
	if json.Unmarshal(version.Definition, &definition) != nil {
		return false
	}
	definitionRaw, err := json.Marshal(definition)
	if err != nil {
		return false
	}
	digest := sha256.Sum256(append(append([]byte{}, metadataRaw...), definitionRaw...))
	return hex.EncodeToString(digest[:]) == version.ChecksumSHA256
}

func loadLatestWorkflowVersionTx(ctx context.Context, tx *sql.Tx, workflowID string) (*WorkflowVersion, error) {
	var versionID string
	if err := tx.QueryRowContext(ctx, `SELECT id FROM space_workflow_versions WHERE workflow_id=$1 ORDER BY created_at DESC,id DESC LIMIT 1`, workflowID).Scan(&versionID); err != nil {
		return nil, err
	}
	return loadWorkflowVersionTx(ctx, tx, versionID)
}

func createDefaultAgentWorkflowTx(ctx context.Context, tx *sql.Tx, userID string, item *SpaceStudioResource) error {
	workflowID := "workflow_" + uuid.NewString()
	stableIdentifier := "space." + item.SpaceID + ".agent." + item.ID
	definition := json.RawMessage(`{"nodes":[{"id":"respond","kind":"structured_prompt","config":{"prompt":"{{input}}"}}],"edges":[]}`)
	if workflowDefinitionHasNodes(item.Definition) {
		definition = item.Definition
	}
	metadata := defaultWorkflowMetadata(item.Name, item.Description, item.RuntimeKind)
	metadataRaw, _ := json.Marshal(metadata)
	digest := sha256.Sum256(append(append([]byte{}, metadataRaw...), definition...))
	versionID := "wfver_" + uuid.NewString()
	if _, err := tx.ExecContext(ctx, `INSERT INTO space_workflows(id,space_id,creator_user_id,name,definition,enabled,stable_identifier,description,author_name,tags,suggested_agent_preset,source_kind)
		VALUES($1,$2,$3,$4,$5,TRUE,$6,$7,'Misty','["agent"]'::jsonb,$8,'custom')`, workflowID, item.SpaceID, userID, item.Name+" Workflow", definition, stableIdentifier, item.Description, mustJSON(SuggestedAgentPreset{Name: item.Name, Icon: item.Icon, Description: item.Description, Instructions: item.Instructions})); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO space_workflow_versions(id,workflow_id,space_id,stable_identifier,version,name,description,author_name,metadata,definition,checksum_sha256,created_by_user_id)
		VALUES($1,$2,$3,$4,'1.0.0',$5,$6,'Misty',$7,$8,$9,$10)`, versionID, workflowID, item.SpaceID, stableIdentifier, item.Name+" Workflow", item.Description, metadataRaw, definition, hex.EncodeToString(digest[:]), userID); err != nil {
		return err
	}
	item.ActiveWorkflowVersionID = versionID
	return nil
}

func workflowDefinitionHasNodes(definition json.RawMessage) bool {
	var parsed struct {
		Nodes []json.RawMessage `json:"nodes"`
	}
	return json.Unmarshal(definition, &parsed) == nil && len(parsed.Nodes) > 0
}

func snapshotWorkflowTx(ctx context.Context, tx *sql.Tx, userID string, item *SpaceStudioResource) (*WorkflowVersion, error) {
	var stableIdentifier, description, authorName string
	if err := tx.QueryRowContext(ctx, `SELECT stable_identifier,description,author_name FROM space_workflows WHERE id=$1 AND space_id=$2`, item.ID, item.SpaceID).Scan(&stableIdentifier, &description, &authorName); err != nil {
		return nil, err
	}
	metadata, explicitMetadata, err := metadataFromWorkflowDefinition(item.Name, description, item.Definition)
	if err != nil {
		return nil, err
	}
	if !explicitMetadata && item.ActiveWorkflow != nil && ValidateWorkflowMetadata(item.ActiveWorkflow.Metadata) == nil {
		metadata = item.ActiveWorkflow.Metadata
	}
	metadataRaw, _ := json.Marshal(metadata)
	digest := sha256.Sum256(append(append([]byte{}, metadataRaw...), item.Definition...))
	checksum := hex.EncodeToString(digest[:])
	version := fmt.Sprintf("1.0.%d", maxInt64(item.Version-1, 0))
	out := &WorkflowVersion{}
	err = scanWorkflowVersion(tx.QueryRowContext(ctx, `INSERT INTO space_workflow_versions(id,workflow_id,space_id,stable_identifier,version,name,description,author_name,metadata,definition,checksum_sha256,created_by_user_id)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		ON CONFLICT(workflow_id,checksum_sha256) DO UPDATE SET checksum_sha256=EXCLUDED.checksum_sha256
		RETURNING `+workflowVersionReturningColumns,
		"wfver_"+uuid.NewString(), item.ID, item.SpaceID, stableIdentifier, version, item.Name, description, authorName, metadataRaw, item.Definition, checksum, userID), out)
	return out, err
}

func metadataFromWorkflowDefinition(name, description string, definition json.RawMessage) (WorkflowMetadata, bool, error) {
	var envelope map[string]json.RawMessage
	if json.Unmarshal(definition, &envelope) != nil || envelope == nil {
		return WorkflowMetadata{}, false, ErrSpaceInvalid
	}
	raw, exists := envelope["metadata"]
	if !exists {
		return defaultWorkflowMetadata(name, description, "cloud"), false, nil
	}
	var metadata WorkflowMetadata
	if json.Unmarshal(raw, &metadata) != nil || ValidateWorkflowMetadata(metadata) != nil {
		return WorkflowMetadata{}, true, ErrSpaceInvalid
	}
	return metadata, true, nil
}

func defaultWorkflowMetadata(name, description, runtimeKind string) WorkflowMetadata {
	if strings.TrimSpace(description) == "" {
		description = "Run " + name
	}
	runtime := "misty-cloud"
	if runtimeKind == "device" {
		runtime = "misty-device"
	}
	capability := WorkflowCapability{
		ID: "default", Name: name, Description: description,
		Inputs:  []WorkflowField{{Name: "prompt", Type: "string", Required: true}},
		Outputs: []WorkflowField{{Name: "result", Type: "object"}},
		Tags:    []string{"assistant"},
	}
	permissions := []string{}
	if runtimeKind == "device" {
		capability.Destructive, capability.ConfirmationRequired = true, true
		capability.Tags = []string{"files", "folders"}
		permissions = []string{"files.read", "files.write"}
	}
	return WorkflowMetadata{
		Capabilities:         []WorkflowCapability{capability},
		RequiredIntegrations: []string{}, RequiredPermissions: permissions,
		Runtime: WorkflowRuntime{Kind: runtime, Compatibility: "1"}, Tags: []string{"assistant"},
	}
}

func mustJSON(value any) json.RawMessage {
	raw, _ := json.Marshal(value)
	return raw
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
