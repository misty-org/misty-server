package db

import (
	"context"
	"database/sql"
	"encoding/json"
)

type ClaimedProviderWorkflow struct {
	InstanceID        string
	UserID            string
	SpaceID           string
	AgentID           string
	WorkflowVersionID string
	CapabilityID      string
	Provider          string
	EventID           string
	ResourceID        string
}

// ClaimProviderWorkflows fans one normalized provider signal out to every
// member who explicitly enabled and consented to a matching attached workflow.
// The claim key includes the member-owned instance, so users never share a
// cursor while duplicate deliveries remain idempotent for each user.
func (db *Database) ClaimProviderWorkflows(ctx context.Context, spaceID, provider, resourceID, eventID, fingerprint string, limit int) ([]ClaimedProviderWorkflow, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	claimed := []ClaimedProviderWorkflow{}
	err := db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT i.id,i.user_id,i.space_id,i.agent_id,w.workflow_version_id,w.trigger_config
			FROM space_agent_instance_workflows w JOIN space_agent_instances i ON i.id=w.instance_id
			WHERE i.space_id=$1 AND w.enabled AND w.consent->>'granted'='true'
			AND w.trigger_config->>'kind' IN ('connector_event','provider_event')
			AND w.trigger_config->>'provider'=$2
			AND (COALESCE(w.trigger_config->>'resourceId','')='' OR w.trigger_config->>'resourceId'=$3)
			ORDER BY w.updated_at,i.id LIMIT $4`, spaceID, provider, resourceID, limit*2)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item ClaimedProviderWorkflow
			var config json.RawMessage
			if err := rows.Scan(&item.InstanceID, &item.UserID, &item.SpaceID, &item.AgentID, &item.WorkflowVersionID, &config); err != nil {
				return err
			}
			var values struct {
				CapabilityID string `json:"capabilityId"`
			}
			if json.Unmarshal(config, &values) != nil || values.CapabilityID == "" {
				continue
			}
			result, err := tx.ExecContext(ctx, `INSERT INTO space_workflow_event_claims(instance_id,workflow_version_id,provider,event_id,fingerprint,state)
				VALUES($1,$2,$3,$4,$5,'claimed') ON CONFLICT DO NOTHING`, item.InstanceID, item.WorkflowVersionID, provider, eventID, fingerprint)
			if err != nil {
				return err
			}
			inserted, _ := result.RowsAffected()
			if inserted != 1 {
				continue
			}
			item.CapabilityID, item.Provider, item.EventID, item.ResourceID = values.CapabilityID, provider, eventID, resourceID
			claimed = append(claimed, item)
			if len(claimed) == limit {
				break
			}
		}
		return rows.Err()
	})
	return claimed, err
}

func (db *Database) ClaimTaskWorkflows(ctx context.Context, spaceID, taskID, eventID, fingerprint string, agentCreated bool, limit int) ([]ClaimedProviderWorkflow, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	claimed := []ClaimedProviderWorkflow{}
	err := db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT i.id,i.user_id,i.space_id,i.agent_id,w.workflow_version_id,w.trigger_config
			FROM space_agent_instance_workflows w JOIN space_agent_instances i ON i.id=w.instance_id
			WHERE i.space_id=$1 AND w.enabled AND w.consent->>'granted'='true' AND w.trigger_config->>'kind'='task_change'
			AND (NOT $2 OR w.trigger_config->>'includeAgentChanges'='true')
			ORDER BY w.updated_at,i.id LIMIT $3`, spaceID, agentCreated, limit*2)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item ClaimedProviderWorkflow
			var config json.RawMessage
			if err := rows.Scan(&item.InstanceID, &item.UserID, &item.SpaceID, &item.AgentID, &item.WorkflowVersionID, &config); err != nil {
				return err
			}
			var values struct {
				CapabilityID string `json:"capabilityId"`
			}
			if json.Unmarshal(config, &values) != nil || values.CapabilityID == "" {
				continue
			}
			result, err := tx.ExecContext(ctx, `INSERT INTO space_workflow_event_claims(instance_id,workflow_version_id,provider,event_id,fingerprint,state)
				VALUES($1,$2,'space_tasks',$3,$4,'claimed') ON CONFLICT DO NOTHING`, item.InstanceID, item.WorkflowVersionID, eventID, fingerprint)
			if err != nil {
				return err
			}
			inserted, _ := result.RowsAffected()
			if inserted != 1 {
				continue
			}
			item.CapabilityID, item.Provider, item.EventID, item.ResourceID = values.CapabilityID, "space_tasks", eventID, taskID
			claimed = append(claimed, item)
			if len(claimed) == limit {
				break
			}
		}
		return rows.Err()
	})
	return claimed, err
}
