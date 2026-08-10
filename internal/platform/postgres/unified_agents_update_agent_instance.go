package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"
)

func (db *Database) UpdateAgentInstance(ctx context.Context, userID, instanceID string) (*AgentInstanceRecord, error) {
	out := &AgentInstanceRecord{ConnectionBindings: map[string]string{}}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "agent-instance:"+instanceID); err != nil {
			return err
		}
		var bindingsRaw []byte
		if err := tx.QueryRowContext(ctx, `SELECT id,space_id,agent_id,user_id,agent_version_id,connection_bindings,capability_grants,created_at,updated_at FROM space_agent_instances WHERE id=$1 AND user_id=$2 FOR UPDATE`, instanceID, userID).Scan(&out.ID, &out.SpaceID, &out.AgentID, &out.UserID, &out.AgentVersionID, &bindingsRaw, &out.CapabilityGrants, &out.CreatedAt, &out.UpdatedAt); err != nil {
			return err
		}
		_ = json.Unmarshal(bindingsRaw, &out.ConnectionBindings)
		if err := hydrateAgentInstanceState(ctx, tx, out); err != nil {
			return err
		}
		if out.Status != "idle" {
			return ErrSpaceConflict
		}
		var published string
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(published_agent_version_id,'') FROM space_agents WHERE id=$1`, out.AgentID).Scan(&published); err != nil {
			return err
		}
		if published == "" || published == out.AgentVersionID {
			return nil
		}
		if _, err := tx.ExecContext(ctx, `UPDATE space_agent_instances SET agent_version_id=$1,capability_grants='[]'::jsonb,updated_at=NOW() WHERE id=$2`, published, instanceID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM space_agent_instance_workflows w WHERE instance_id=$1 AND NOT EXISTS(SELECT 1 FROM space_agent_version_workflows a WHERE a.agent_version_id=$2 AND a.workflow_version_id=w.workflow_version_id AND a.enabled)`, instanceID, published); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO space_agent_instance_workflows(instance_id,workflow_version_id,enabled,trigger_config,consent)
			SELECT $1,workflow_version_id,FALSE,'{}'::jsonb,'{}'::jsonb FROM space_agent_version_workflows WHERE agent_version_id=$2 AND enabled
			ON CONFLICT(instance_id,workflow_version_id) DO UPDATE SET enabled=FALSE,consent='{}'::jsonb,updated_at=NOW()`, instanceID, published); err != nil {
			return err
		}
		out.AgentVersionID, out.CapabilityGrants, out.UpdateAvailable, out.UpdatedAt = published, json.RawMessage(`[]`), false, time.Now().UTC()
		return hydrateAgentInstanceState(ctx, tx, out)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceNotFound
	}
	return out, err
}

func (db *Database) ConfigureInstanceWorkflow(ctx context.Context, userID, instanceID, workflowVersionID string, enabled bool, triggerConfig, consent json.RawMessage) (*InstanceWorkflowConfig, error) {
	if !validJSONObject(triggerConfig) || !validJSONObject(consent) {
		return nil, ErrSpaceInvalid
	}
	out := &InstanceWorkflowConfig{WorkflowVersionID: workflowVersionID, Enabled: enabled, TriggerConfig: triggerConfig, Consent: consent, Cursor: json.RawMessage(`{}`)}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		var agentVersionID string
		if err := tx.QueryRowContext(ctx, `SELECT agent_version_id FROM space_agent_instances WHERE id=$1 AND user_id=$2`, instanceID, userID).Scan(&agentVersionID); err != nil {
			return err
		}
		var attached bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM space_agent_version_workflows WHERE agent_version_id=$1 AND workflow_version_id=$2 AND enabled)`, agentVersionID, workflowVersionID).Scan(&attached); err != nil || !attached {
			return ErrSpaceInvalid
		}
		return tx.QueryRowContext(ctx, `INSERT INTO space_agent_instance_workflows(instance_id,workflow_version_id,enabled,trigger_config,consent) VALUES($1,$2,$3,$4,$5) ON CONFLICT(instance_id,workflow_version_id) DO UPDATE SET enabled=EXCLUDED.enabled,trigger_config=EXCLUDED.trigger_config,consent=EXCLUDED.consent,updated_at=NOW() RETURNING cursor,updated_at`, instanceID, workflowVersionID, enabled, triggerConfig, consent).Scan(&out.Cursor, &out.UpdatedAt)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceNotFound
	}
	return out, err
}

func (db *Database) UpdateAgentInstanceConnections(ctx context.Context, userID, instanceID string, bindings map[string]string) (*AgentInstanceRecord, error) {
	if bindings == nil {
		bindings = map[string]string{}
	}
	for provider, connectionID := range bindings {
		if !validWorkflowToken(provider, 120) || strings.TrimSpace(connectionID) == "" {
			return nil, ErrSpaceInvalid
		}
	}
	raw, _ := json.Marshal(bindings)
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		var spaceID string
		if err := tx.QueryRowContext(ctx, `SELECT space_id FROM space_agent_instances WHERE id=$1 AND user_id=$2`, instanceID, userID).Scan(&spaceID); err != nil {
			return err
		}
		for _, connectionID := range bindings {
			var valid bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM space_integrations WHERE id=$1 AND space_id=$2 AND connected_by_user_id=$3 AND status='active')`, connectionID, spaceID, userID).Scan(&valid); err != nil || !valid {
				return ErrSpaceInvalid
			}
		}
		_, err := tx.ExecContext(ctx, `UPDATE space_agent_instances SET connection_bindings=$1,updated_at=NOW() WHERE id=$2 AND user_id=$3`, raw, instanceID, userID)
		return err
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceNotFound
	}
	if err != nil {
		return nil, err
	}
	return db.agentInstanceByID(ctx, userID, instanceID)
}

func (db *Database) agentInstanceByID(ctx context.Context, userID, instanceID string) (*AgentInstanceRecord, error) {
	out := &AgentInstanceRecord{ConnectionBindings: map[string]string{}}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		var bindingsRaw []byte
		if err := tx.QueryRowContext(ctx, `SELECT id,space_id,agent_id,user_id,agent_version_id,connection_bindings,capability_grants,created_at,updated_at FROM space_agent_instances WHERE id=$1 AND user_id=$2`, instanceID, userID).Scan(&out.ID, &out.SpaceID, &out.AgentID, &out.UserID, &out.AgentVersionID, &bindingsRaw, &out.CapabilityGrants, &out.CreatedAt, &out.UpdatedAt); err != nil {
			return err
		}
		_ = json.Unmarshal(bindingsRaw, &out.ConnectionBindings)
		return hydrateAgentInstanceState(ctx, tx, out)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceNotFound
	}
	return out, err
}

func (db *Database) AgentInstanceByID(ctx context.Context, userID, instanceID string) (*AgentInstanceRecord, error) {
	return db.agentInstanceByID(ctx, userID, instanceID)
}

func (db *Database) PublishedAgentVersions(ctx context.Context, userID, spaceID, agentID string) ([]PublishedAgentVersion, error) {
	items := []PublishedAgentVersion{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT id,agent_id,space_id,creator_user_id,version,name,description,icon,instructions,access_policy,checksum_sha256,published_at FROM space_agent_versions WHERE space_id=$1 AND agent_id=$2 ORDER BY version DESC`, spaceID, agentID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item PublishedAgentVersion
			var accessRaw []byte
			if err := rows.Scan(&item.ID, &item.AgentID, &item.SpaceID, &item.CreatorID, &item.Version, &item.Name, &item.Description, &item.Icon, &item.Instructions, &accessRaw, &item.Checksum, &item.PublishedAt); err != nil {
				return err
			}
			_ = json.Unmarshal(accessRaw, &item.Access)
			item.Workflows = []AgentVersionWorkflow{}
			child, err := tx.QueryContext(ctx, `SELECT workflow_version_id,alias,enabled,position FROM space_agent_version_workflows WHERE agent_version_id=$1 ORDER BY position,alias`, item.ID)
			if err != nil {
				return err
			}
			for child.Next() {
				var attached AgentVersionWorkflow
				if err := child.Scan(&attached.WorkflowVersionID, &attached.Alias, &attached.Enabled, &attached.Position); err != nil {
					child.Close()
					return err
				}
				item.Workflows = append(item.Workflows, attached)
			}
			if err := child.Close(); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	sort.SliceStable(items, func(i, j int) bool { return items[i].Version > items[j].Version })
	return items, err
}
