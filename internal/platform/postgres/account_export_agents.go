package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

type AccountExportAgent struct {
	ID              string                         `json:"id"`
	Name            string                         `json:"name"`
	Role            string                         `json:"role"`
	Description     string                         `json:"description"`
	Icon            string                         `json:"icon"`
	Avatar          json.RawMessage                `json:"avatar"`
	Instructions    string                         `json:"instructions"`
	ModelMode       string                         `json:"model_mode"`
	ModelID         string                         `json:"model_id,omitempty"`
	ReasoningEffort string                         `json:"reasoning_effort,omitempty"`
	DefaultRunMode  string                         `json:"default_run_mode"`
	Enabled         bool                           `json:"enabled"`
	Version         int64                          `json:"version"`
	Versions        []AccountExportAgentVersion    `json:"versions"`
	// SpaceID is the one Space the agent lives in; Memberships holds its
	// membership there.
	SpaceID     string                         `json:"space_id,omitempty"`
	Memberships []AccountExportAgentMembership `json:"space_memberships"`
	CreatedAt   time.Time                      `json:"created_at"`
	UpdatedAt   time.Time                      `json:"updated_at"`
	DeletedAt   *time.Time                     `json:"deleted_at,omitempty"`
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
	DefaultRunMode  string          `json:"default_run_mode"`
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
	SpaceName         string     `json:"space_name,omitempty"`
	// AcceptPolicy is how the agent takes requests from the Space: ask, auto or off.
	AcceptPolicy    string `json:"accept_policy,omitempty"`
	MaxOpenRequests int    `json:"max_open_requests,omitempty"`
	Description     string `json:"description,omitempty"`
}

func appendAccountAgentExport(ctx context.Context, tx *sql.Tx, userID string, out *AccountPortableExport) error {
	return exportOwnedAgents(ctx, tx, userID, out)
}

func exportOwnedAgents(ctx context.Context, tx *sql.Tx, userID string, out *AccountPortableExport) error {
	rows, err := tx.QueryContext(ctx, `SELECT id,name,role,description,icon,avatar,instructions,model_mode,model_id,
		reasoning_effort,default_run_mode,enabled,version,created_at,updated_at,deleted_at,COALESCE(space_id,'')
		FROM misty_ask_identities WHERE owner_user_id=$1 ORDER BY created_at`, userID)
	if err != nil {
		return err
	}
	items := []AccountExportAgent{}
	for rows.Next() {
		var item AccountExportAgent
		if err := rows.Scan(&item.ID, &item.Name, &item.Role, &item.Description, &item.Icon, &item.Avatar, &item.Instructions, &item.ModelMode,
			&item.ModelID, &item.ReasoningEffort, &item.DefaultRunMode, &item.Enabled,
			&item.Version, &item.CreatedAt, &item.UpdatedAt, &item.DeletedAt, &item.SpaceID); err != nil {
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
		reasoning_effort,default_run_mode,checksum_sha256,created_at FROM misty_ask_identity_versions WHERE agent_id=$1 ORDER BY version`, agent.ID)
	if err != nil {
		return err
	}
	for versions.Next() {
		var item AccountExportAgentVersion
		if err := versions.Scan(&item.ID, &item.Version, &item.Name, &item.Role, &item.Description, &item.Icon, &item.Avatar, &item.Instructions,
			&item.ModelMode, &item.ModelID, &item.ReasoningEffort, &item.DefaultRunMode, &item.ChecksumSHA256, &item.CreatedAt); err != nil {
			versions.Close()
			return err
		}
		agent.Versions = append(agent.Versions, item)
	}
	if err := versions.Close(); err != nil {
		return err
	}
	// An agent's listing in its Space is its membership there.
	memberships, err := tx.QueryContext(ctx, `SELECT l.space_id,s.name,l.accept_policy,l.max_open_requests,l.description,l.created_at,l.updated_at
		FROM space_agent_listings l JOIN spaces s ON s.id=l.space_id WHERE l.agent_id=$1 ORDER BY l.created_at`, agent.ID)
	if err != nil {
		return err
	}
	defer memberships.Close()
	for memberships.Next() {
		item := AccountExportAgentMembership{SpaceRole: "agent", Version: agent.Version, ApprovedVersionID: personalAgentVersionID(agent.ID, agent.Version)}
		if err := memberships.Scan(&item.SpaceID, &item.SpaceName, &item.AcceptPolicy, &item.MaxOpenRequests, &item.Description, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return err
		}
		item.ID = "agentmember:" + item.SpaceID + ":" + agent.ID
		item.Enabled = item.AcceptPolicy != "off"
		agent.Memberships = append(agent.Memberships, item)
	}
	return memberships.Err()
}
