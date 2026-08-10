package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strings"
)

type AgentCapabilityGrant struct {
	Capability string            `json:"capability"`
	Risk       string            `json:"risk"`
	Scopes     map[string]string `json:"scopes,omitempty"`
}

var defaultAgentCapabilityGrants = []AgentCapabilityGrant{
	{Capability: "calendar.query", Risk: "read"},
	{Capability: "library.search", Risk: "read"},
	{Capability: "messages.search", Risk: "read"},
	{Capability: "provider.discord.query", Risk: "read"},
	{Capability: "provider.discord.write", Risk: "write"},
	{Capability: "provider.google.query", Risk: "read"},
	{Capability: "provider.notion.query", Risk: "read"},
	{Capability: "provider.slack.query", Risk: "read"},
	{Capability: "provider.slack.write", Risk: "write"},
	{Capability: "tasks.create", Risk: "write"},
	{Capability: "tasks.query", Risk: "read"},
	{Capability: "tasks.update", Risk: "write"},
}

func DefaultAgentCapabilityGrants() json.RawMessage {
	raw, _ := json.Marshal(defaultAgentCapabilityGrants)
	return raw
}

func normalizeAgentCapabilityGrants(raw json.RawMessage) (json.RawMessage, error) {
	var grants []AgentCapabilityGrant
	if len(raw) == 0 || json.Unmarshal(raw, &grants) != nil || grants == nil {
		return nil, ErrSpaceInvalid
	}
	seen := map[string]bool{}
	for index := range grants {
		grant := &grants[index]
		grant.Capability = strings.TrimSpace(grant.Capability)
		grant.Risk = strings.TrimSpace(grant.Risk)
		if !validWorkflowToken(grant.Capability, 160) || grant.Risk != "read" && grant.Risk != "write" && grant.Risk != "dangerous" || seen[grant.Capability] {
			return nil, ErrSpaceInvalid
		}
		seen[grant.Capability] = true
		normalizedScopes := make(map[string]string, len(grant.Scopes))
		for key, value := range grant.Scopes {
			key, value = strings.TrimSpace(key), strings.TrimSpace(value)
			if !validWorkflowToken(key, 80) || value == "" || len(value) > 500 {
				return nil, ErrSpaceInvalid
			}
			normalizedScopes[key] = value
		}
		if len(normalizedScopes) > 0 {
			grant.Scopes = normalizedScopes
		} else {
			grant.Scopes = nil
		}
	}
	sort.Slice(grants, func(i, j int) bool { return grants[i].Capability < grants[j].Capability })
	return json.Marshal(grants)
}

func TestingNormalizeAgentCapabilityGrants(raw json.RawMessage) (json.RawMessage, error) {
	return normalizeAgentCapabilityGrants(raw)
}

func AgentCapabilityGranted(raw json.RawMessage, capability, risk string) bool {
	var grants []AgentCapabilityGrant
	if json.Unmarshal(raw, &grants) != nil {
		return false
	}
	capability, risk = strings.TrimSpace(capability), strings.TrimSpace(risk)
	for _, grant := range grants {
		if grant.Capability == capability && grant.Risk == risk {
			return true
		}
	}
	return false
}

func (db *Database) AgentInstanceCapabilityAllowed(ctx context.Context, userID, instanceID, capability, risk string) (bool, error) {
	var raw json.RawMessage
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT capability_grants FROM space_agent_instances WHERE id=$1 AND user_id=$2`, instanceID, userID).Scan(&raw)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrSpaceNotFound
	}
	return err == nil && AgentCapabilityGranted(raw, capability, risk), err
}

func (db *Database) UpdateAgentInstanceCapabilityGrants(ctx context.Context, userID, instanceID string, grants json.RawMessage) (*AgentInstanceRecord, error) {
	normalized, err := normalizeAgentCapabilityGrants(grants)
	if err != nil {
		return nil, err
	}
	err = db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		var spaceID, agentID string
		if lookupErr := tx.QueryRowContext(ctx, `SELECT space_id,agent_id FROM space_agent_instances WHERE id=$1 AND user_id=$2 FOR UPDATE`, instanceID, userID).Scan(&spaceID, &agentID); lookupErr != nil {
			if errors.Is(lookupErr, sql.ErrNoRows) {
				return ErrSpaceNotFound
			}
			return lookupErr
		}
		result, updateErr := tx.ExecContext(ctx, `UPDATE space_agent_instances SET capability_grants=$1,updated_at=NOW() WHERE id=$2 AND user_id=$3`, normalized, instanceID, userID)
		if updateErr != nil {
			return updateErr
		}
		changed, updateErr := result.RowsAffected()
		if updateErr != nil {
			return updateErr
		}
		if changed != 1 {
			return ErrSpaceNotFound
		}
		_, updateErr = recordSpaceEventTx(ctx, tx, spaceID, userID, "agent.instance.capabilities.updated", agentID, map[string]any{"instance_id": instanceID, "grants": json.RawMessage(normalized)})
		return updateErr
	})
	if err != nil {
		return nil, err
	}
	return db.agentInstanceByID(ctx, userID, instanceID)
}
