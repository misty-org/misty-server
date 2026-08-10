package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

type AccountExportAgent struct {
	ID                 string                         `json:"id"`
	Name               string                         `json:"name"`
	Role               string                         `json:"role"`
	Description        string                         `json:"description"`
	Icon               string                         `json:"icon"`
	Avatar             json.RawMessage                `json:"avatar"`
	Instructions       string                         `json:"instructions"`
	ModelMode          string                         `json:"model_mode"`
	ModelID            string                         `json:"model_id,omitempty"`
	ReasoningEffort    string                         `json:"reasoning_effort,omitempty"`
	ContextPermissions json.RawMessage                `json:"context_permissions"`
	ToolPermissions    json.RawMessage                `json:"tool_permissions"`
	Enabled            bool                           `json:"enabled"`
	Version            int64                          `json:"version"`
	Versions           []AccountExportAgentVersion    `json:"versions"`
	Memberships        []AccountExportAgentMembership `json:"space_memberships"`
	CreatedAt          time.Time                      `json:"created_at"`
	UpdatedAt          time.Time                      `json:"updated_at"`
	DeletedAt          *time.Time                     `json:"deleted_at,omitempty"`
}

type AccountExportAgentVersion struct {
	ID              string          `json:"id"`
	Version         int64           `json:"version"`
	Name            string          `json:"name"`
	Role            string          `json:"role"`
	Description     string          `json:"description"`
	Icon            string          `json:"icon"`
	Avatar          json.RawMessage `json:"avatar"`
	Instructions    string          `json:"instructions"`
	ModelMode       string          `json:"model_mode"`
	ModelID         string          `json:"model_id,omitempty"`
	ReasoningEffort string          `json:"reasoning_effort,omitempty"`
	ChecksumSHA256  string          `json:"checksum_sha256"`
	CreatedAt       time.Time       `json:"created_at"`
}

type AccountExportAgentMembership struct {
	ID                string     `json:"id"`
	SpaceID           string     `json:"space_id"`
	Enabled           bool       `json:"enabled"`
	ApprovedVersionID string     `json:"approved_version_id"`
	SpaceRole         string     `json:"space_role"`
	Version           int64      `json:"version"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
	RemovedAt         *time.Time `json:"removed_at,omitempty"`
}

type AccountExportAgentEvent struct {
	ID        int64           `json:"id"`
	Type      string          `json:"type"`
	Data      json.RawMessage `json:"data"`
	CreatedAt time.Time       `json:"created_at"`
}

type AccountExportAgentConversation struct {
	ID              string                    `json:"id"`
	PersonalAgentID string                    `json:"agent_id,omitempty"`
	SpaceID         string                    `json:"space_id,omitempty"`
	ModelID         string                    `json:"model_id,omitempty"`
	State           json.RawMessage           `json:"state"`
	Events          []AccountExportAgentEvent `json:"events"`
	CreatedAt       time.Time                 `json:"created_at"`
	UpdatedAt       time.Time                 `json:"updated_at"`
	DeletedAt       *time.Time                `json:"deleted_at,omitempty"`
}

type AccountExportAgentMemory struct {
	AgentID      string          `json:"agent_id"`
	SpaceID      string          `json:"space_id,omitempty"`
	ScopeKey     string          `json:"scope_key"`
	Memory       json.RawMessage `json:"memory"`
	LegacyMemory json.RawMessage `json:"legacy_memory"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
}

func appendAccountAgentExport(ctx context.Context, tx *sql.Tx, userID string, out *AccountPortableExport) error {
	return exportOwnedAgents(ctx, tx, userID, out)
}

func exportOwnedAgents(ctx context.Context, tx *sql.Tx, userID string, out *AccountPortableExport) error {
	rows, err := tx.QueryContext(ctx, `SELECT id,name,role,description,icon,avatar,instructions,model_mode,model_id,
		reasoning_effort,context_permissions,tool_permissions,enabled,version,created_at,updated_at,deleted_at
		FROM personal_agents WHERE owner_user_id=$1 ORDER BY created_at`, userID)
	if err != nil {
		return err
	}
	items := []AccountExportAgent{}
	for rows.Next() {
		var item AccountExportAgent
		if err := rows.Scan(&item.ID, &item.Name, &item.Role, &item.Description, &item.Icon, &item.Avatar, &item.Instructions, &item.ModelMode,
			&item.ModelID, &item.ReasoningEffort, &item.ContextPermissions, &item.ToolPermissions, &item.Enabled,
			&item.Version, &item.CreatedAt, &item.UpdatedAt, &item.DeletedAt); err != nil {
			return err
		}
		items = append(items, item)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for index := range items {
		items[index].Versions = []AccountExportAgentVersion{}
		items[index].Memberships = []AccountExportAgentMembership{}
		if err := exportAgentVersionsAndMemberships(ctx, tx, &items[index]); err != nil {
			return err
		}
	}
	out.Agents = append(out.Agents, items...)
	return nil
}

func exportAgentVersionsAndMemberships(ctx context.Context, tx *sql.Tx, agent *AccountExportAgent) error {
	versions, err := tx.QueryContext(ctx, `SELECT id,version,name,role,description,icon,avatar,instructions,model_mode,model_id,
		reasoning_effort,checksum_sha256,created_at FROM personal_agent_versions WHERE agent_id=$1 ORDER BY version`, agent.ID)
	if err != nil {
		return err
	}
	for versions.Next() {
		var item AccountExportAgentVersion
		if err := versions.Scan(&item.ID, &item.Version, &item.Name, &item.Role, &item.Description, &item.Icon, &item.Avatar, &item.Instructions,
			&item.ModelMode, &item.ModelID, &item.ReasoningEffort, &item.ChecksumSHA256, &item.CreatedAt); err != nil {
			versions.Close()
			return err
		}
		agent.Versions = append(agent.Versions, item)
	}
	if err := versions.Close(); err != nil {
		return err
	}
	memberships, err := tx.QueryContext(ctx, `SELECT id,space_id,enabled,approved_version_id,space_role,
		version,created_at,updated_at,removed_at FROM personal_agent_space_grants WHERE agent_id=$1 ORDER BY created_at`, agent.ID)
	if err != nil {
		return err
	}
	defer memberships.Close()
	for memberships.Next() {
		var item AccountExportAgentMembership
		if err := memberships.Scan(&item.ID, &item.SpaceID, &item.Enabled, &item.ApprovedVersionID, &item.SpaceRole,
			&item.Version, &item.CreatedAt, &item.UpdatedAt, &item.RemovedAt); err != nil {
			return err
		}
		agent.Memberships = append(agent.Memberships, item)
	}
	return memberships.Err()
}
