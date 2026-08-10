package db

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	ErrPersonalAgentNotFound = errors.New("personal agent not found")
	ErrPersonalAgentConflict = errors.New("personal agent version conflict")
	ErrPersonalAgentModel    = errors.New("personal agent model unavailable")
)

type PersonalAgent struct {
	ID                 string          `json:"id"`
	OwnerUserID        string          `json:"owner_user_id"`
	Name               string          `json:"name"`
	Role               string          `json:"role"`
	Description        string          `json:"description"`
	Icon               string          `json:"icon"`
	Avatar             json.RawMessage `json:"avatar"`
	Instructions       string          `json:"instructions,omitempty"`
	ModelMode          string          `json:"model_mode"`
	ModelID            string          `json:"model_id,omitempty"`
	ReasoningEffort    string          `json:"reasoning_effort,omitempty"`
	ContextPermissions json.RawMessage `json:"context_permissions"`
	ToolPermissions    json.RawMessage `json:"tool_permissions"`
	Enabled            bool            `json:"enabled"`
	Version            int64           `json:"version"`
	LatestVersionID    string          `json:"latest_version_id,omitempty"`
	CreatedAt          time.Time       `json:"created_at"`
	UpdatedAt          time.Time       `json:"updated_at"`
}

type PersonalAgentVersion struct {
	ID              string          `json:"id"`
	AgentID         string          `json:"agent_id"`
	Version         int64           `json:"version"`
	Name            string          `json:"name"`
	Role            string          `json:"role"`
	Description     string          `json:"description"`
	Icon            string          `json:"icon"`
	Avatar          json.RawMessage `json:"avatar"`
	Instructions    string          `json:"instructions,omitempty"`
	ModelMode       string          `json:"model_mode"`
	ModelID         string          `json:"model_id,omitempty"`
	ReasoningEffort string          `json:"reasoning_effort,omitempty"`
	ChecksumSHA256  string          `json:"checksum_sha256"`
	CreatedByUserID string          `json:"created_by_user_id"`
	CreatedAt       time.Time       `json:"created_at"`
}

type PersonalAgentSpaceGrant struct {
	ID                string          `json:"id"`
	AgentID           string          `json:"agent_id"`
	SpaceID           string          `json:"space_id"`
	SpaceName         string          `json:"space_name"`
	AllMembers        bool            `json:"all_members"`
	MemberUserIDs     []string        `json:"member_user_ids"`
	Enabled           bool            `json:"enabled"`
	ApprovedVersionID string          `json:"approved_version_id"`
	LatestVersionID   string          `json:"latest_version_id"`
	UpdateAvailable   bool            `json:"update_available"`
	SpaceInstructions string          `json:"space_instructions"`
	SpaceRole         string          `json:"space_role"`
	Permissions       json.RawMessage `json:"permissions"`
	ManagedByUserID   string          `json:"managed_by_user_id,omitempty"`
	Version           int64           `json:"version"`
	CreatedAt         time.Time       `json:"created_at"`
	UpdatedAt         time.Time       `json:"updated_at"`
}

type PersonalAgentGrantInput struct {
	SpaceID       string   `json:"space_id"`
	AllMembers    bool     `json:"all_members"`
	MemberUserIDs []string `json:"member_user_ids"`
}

const personalAgentColumns = `id,owner_user_id,name,role,description,icon,avatar,instructions,model_mode,model_id,reasoning_effort,context_permissions,tool_permissions,enabled,version,created_at,updated_at`

func scanPersonalAgent(row scanner, out *PersonalAgent) error {
	err := row.Scan(&out.ID, &out.OwnerUserID, &out.Name, &out.Role, &out.Description, &out.Icon, &out.Avatar, &out.Instructions,
		&out.ModelMode, &out.ModelID, &out.ReasoningEffort, &out.ContextPermissions, &out.ToolPermissions, &out.Enabled,
		&out.Version, &out.CreatedAt, &out.UpdatedAt)
	if err == nil {
		out.LatestVersionID = personalAgentVersionID(out.ID, out.Version)
	}
	return err
}

func personalAgentVersionID(agentID string, version int64) string {
	return fmt.Sprintf("personalver_%x", md5.Sum([]byte(fmt.Sprintf("%s:%d", agentID, version))))
}

func insertPersonalAgentVersionTx(ctx context.Context, tx *sql.Tx, agent PersonalAgent, userID string) (string, error) {
	id := personalAgentVersionID(agent.ID, agent.Version)
	checksum := fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s", agent.ID, agent.Version, agent.Name, agent.Role, agent.Description, agent.Instructions, agent.ModelID, agent.ReasoningEffort, agent.Icon, string(agent.Avatar), string(agent.ContextPermissions), string(agent.ToolPermissions)))))
	_, err := tx.ExecContext(ctx, `INSERT INTO personal_agent_versions(id,agent_id,version,name,role,description,icon,avatar,instructions,model_mode,model_id,reasoning_effort,context_permissions,tool_permissions,checksum_sha256,created_by_user_id)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16) ON CONFLICT(agent_id,version) DO NOTHING`, id, agent.ID, agent.Version, agent.Name, agent.Role, agent.Description, agent.Icon, agent.Avatar, agent.Instructions, agent.ModelMode, agent.ModelID, agent.ReasoningEffort, agent.ContextPermissions, agent.ToolPermissions, checksum, userID)
	return id, err
}

func TestingNormalizePersonalAgent(agent *PersonalAgent) error {
	agent.Name = strings.TrimSpace(agent.Name)
	agent.Role = strings.TrimSpace(agent.Role)
	agent.Description = strings.TrimSpace(agent.Description)
	agent.Icon = strings.TrimSpace(agent.Icon)
	agent.Instructions = strings.TrimSpace(agent.Instructions)
	agent.ModelMode = strings.ToLower(strings.TrimSpace(agent.ModelMode))
	agent.ModelID = strings.TrimSpace(agent.ModelID)
	agent.ReasoningEffort = strings.ToLower(strings.TrimSpace(agent.ReasoningEffort))
	switch agent.ReasoningEffort {
	case "", "low", "medium", "high":
	default:
		agent.ReasoningEffort = ""
	}
	if agent.ModelMode == "" {
		agent.ModelMode = "pinned"
	}
	if len([]rune(agent.Name)) < 1 || len([]rune(agent.Name)) > 80 || len([]rune(agent.Role)) > 80 || len([]rune(agent.Description)) > 400 || len([]rune(agent.Instructions)) > 16_000 {
		return ErrSpaceInvalid
	}
	avatar, err := normalizePersonalAgentAvatar(agent.Avatar, agent.Icon)
	if err != nil {
		return err
	}
	agent.Avatar = avatar
	if agent.ModelMode != "pinned" || agent.ModelID == "" {
		return ErrSpaceInvalid
	}
	if !validPersonalJSONObject(agent.ContextPermissions) {
		// "task_notes" is the current name for the notes column on a task. Rows
		// stored under the old "notes" key still work; see the alias in
		// PersonalAgentSpaceContextForConversation.
		agent.ContextPermissions = json.RawMessage(`{"space_chat":true,"library":true,"task_notes":true,"tasks":true,"members":true}`)
	}
	if !validPersonalJSONObject(agent.ToolPermissions) {
		agent.ToolPermissions = json.RawMessage(`{"read":true,"write":false,"integrations":[]}`)
	}
	return nil
}

func validPersonalJSONObject(raw json.RawMessage) bool {
	var value map[string]any
	return len(raw) > 0 && json.Unmarshal(raw, &value) == nil && value != nil
}

func normalizePersonalAgentAvatar(raw json.RawMessage, legacyIcon string) (json.RawMessage, error) {
	if len(raw) == 0 || string(raw) == "null" {
		preset := strings.TrimSpace(legacyIcon)
		if preset == "" {
			preset = "bot"
		}
		return json.Marshal(map[string]any{"kind": "preset", "preset_id": preset, "accent": "indigo"})
	}
	var value struct {
		Kind     string `json:"kind"`
		PresetID string `json:"preset_id"`
		Accent   string `json:"accent"`
		AssetID  string `json:"asset_id"`
		Version  int64  `json:"version"`
	}
	if json.Unmarshal(raw, &value) != nil {
		return nil, ErrSpaceInvalid
	}
	value.Kind = strings.ToLower(strings.TrimSpace(value.Kind))
	value.PresetID = strings.TrimSpace(value.PresetID)
	value.Accent = strings.TrimSpace(value.Accent)
	value.AssetID = strings.TrimSpace(value.AssetID)
	switch value.Kind {
	case "preset":
		if value.PresetID == "" || len([]rune(value.PresetID)) > 80 || len([]rune(value.Accent)) > 40 {
			return nil, ErrSpaceInvalid
		}
		if value.Accent == "" {
			value.Accent = "indigo"
		}
		return json.Marshal(map[string]any{"kind": value.Kind, "preset_id": value.PresetID, "accent": value.Accent})
	case "upload":
		if !strings.HasPrefix(value.AssetID, "agent-avatar_") || len(value.AssetID) > 160 || value.Version < 1 {
			return nil, ErrSpaceInvalid
		}
		return json.Marshal(map[string]any{"kind": value.Kind, "asset_id": value.AssetID, "version": value.Version})
	default:
		return nil, ErrSpaceInvalid
	}
}

func TestingNormalizePersonalAgentAvatar(raw json.RawMessage, legacyIcon string) (json.RawMessage, error) {
	return normalizePersonalAgentAvatar(raw, legacyIcon)
}

func (db *Database) ListPersonalAgents(ctx context.Context, userID string) ([]PersonalAgent, error) {
	items := []PersonalAgent{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT `+personalAgentColumns+` FROM personal_agents WHERE owner_user_id=$1 AND deleted_at IS NULL ORDER BY lower(name),id`, userID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item PersonalAgent
			if err := scanPersonalAgent(rows, &item); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}

func (db *Database) PersonalAgentByID(ctx context.Context, userID, agentID string) (*PersonalAgent, error) {
	out := &PersonalAgent{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		err := scanPersonalAgent(tx.QueryRowContext(ctx, `SELECT `+personalAgentColumns+` FROM personal_agents WHERE id=$1 AND owner_user_id=$2 AND deleted_at IS NULL`, agentID, userID), out)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrPersonalAgentNotFound
		}
		return err
	})
	return out, err
}

func (db *Database) CreatePersonalAgent(ctx context.Context, userID string, item PersonalAgent) (*PersonalAgent, error) {
	item.OwnerUserID = userID
	item.ID = "personal_" + uuid.NewString()
	if err := TestingNormalizePersonalAgent(&item); err != nil {
		return nil, err
	}
	if !item.Enabled {
		item.Enabled = true
	}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := scanPersonalAgent(tx.QueryRowContext(ctx, `INSERT INTO personal_agents(id,owner_user_id,name,role,description,icon,avatar,instructions,model_mode,model_id,reasoning_effort,context_permissions,tool_permissions,enabled)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14) RETURNING `+personalAgentColumns,
			item.ID, userID, item.Name, item.Role, item.Description, item.Icon, item.Avatar, item.Instructions, item.ModelMode, item.ModelID, item.ReasoningEffort, item.ContextPermissions, item.ToolPermissions, item.Enabled), &item); err != nil {
			return err
		}
		_, err := insertPersonalAgentVersionTx(ctx, tx, item, userID)
		return err
	})
	return &item, err
}

func (db *Database) UpdatePersonalAgent(ctx context.Context, userID string, item PersonalAgent) (*PersonalAgent, error) {
	if item.ID == "" || item.Version < 1 {
		return nil, ErrSpaceInvalid
	}
	if err := TestingNormalizePersonalAgent(&item); err != nil {
		return nil, err
	}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		err := scanPersonalAgent(tx.QueryRowContext(ctx, `UPDATE personal_agents SET name=$1,role=$2,description=$3,icon=$4,avatar=$5,instructions=$6,model_mode=$7,model_id=$8,reasoning_effort=$9,context_permissions=$10,tool_permissions=$11,enabled=$12,version=version+1,updated_at=NOW()
			WHERE id=$13 AND owner_user_id=$14 AND version=$15 AND deleted_at IS NULL RETURNING `+personalAgentColumns,
			item.Name, item.Role, item.Description, item.Icon, item.Avatar, item.Instructions, item.ModelMode, item.ModelID, item.ReasoningEffort, item.ContextPermissions, item.ToolPermissions, item.Enabled, item.ID, userID, item.Version), &item)
		if err == nil {
			if !item.Enabled {
				_, _ = tx.ExecContext(ctx, `UPDATE space_runs SET state='canceled',canceled_at=NOW(),completed_at=NOW(),updated_at=NOW()
					WHERE agent_id=$1 AND state IN ('queued','running','cooldown','awaiting_approval')`, item.ID)
			}
			_, err = insertPersonalAgentVersionTx(ctx, tx, item, userID)
			return err
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var exists bool
		if queryErr := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM personal_agents WHERE id=$1 AND owner_user_id=$2 AND deleted_at IS NULL)`, item.ID, userID).Scan(&exists); queryErr != nil {
			return queryErr
		}
		if exists {
			return ErrPersonalAgentConflict
		}
		return ErrPersonalAgentNotFound
	})
	return &item, err
}

func (db *Database) DeletePersonalAgent(ctx context.Context, userID, agentID string) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE personal_agents SET enabled=FALSE,deleted_at=NOW(),version=version+1,updated_at=NOW() WHERE id=$1 AND owner_user_id=$2 AND deleted_at IS NULL`, agentID, userID)
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if count == 0 {
			return ErrPersonalAgentNotFound
		}
		_, _ = tx.ExecContext(ctx, `UPDATE space_runs SET state='canceled',canceled_at=NOW(),completed_at=NOW(),updated_at=NOW()
			WHERE agent_id=$1 AND state IN ('queued','running','cooldown','awaiting_approval')`, agentID)
		return nil
	})
}

func (db *Database) PersonalAgentGrants(ctx context.Context, userID, agentID string) ([]PersonalAgentSpaceGrant, error) {
	items := []PersonalAgentSpaceGrant{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		var owner string
		if err := tx.QueryRowContext(ctx, `SELECT owner_user_id FROM personal_agents WHERE id=$1 AND deleted_at IS NULL`, agentID).Scan(&owner); errors.Is(err, sql.ErrNoRows) {
			return ErrPersonalAgentNotFound
		} else if err != nil {
			return err
		}
		if owner != userID {
			return ErrSpaceForbidden
		}
		rows, err := tx.QueryContext(ctx, `SELECT g.id,g.agent_id,g.space_id,s.name,g.all_members,g.space_role,g.created_at,g.updated_at FROM personal_agent_space_grants g JOIN spaces s ON s.id=g.space_id WHERE g.agent_id=$1 AND g.removed_at IS NULL ORDER BY lower(s.name),g.id`, agentID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var item PersonalAgentSpaceGrant
			if err := rows.Scan(&item.ID, &item.AgentID, &item.SpaceID, &item.SpaceName, &item.AllMembers, &item.SpaceRole, &item.CreatedAt, &item.UpdatedAt); err != nil {
				rows.Close()
				return err
			}
			items = append(items, item)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
		for index := range items {
			item := &items[index]
			memberRows, err := tx.QueryContext(ctx, `SELECT user_id FROM personal_agent_member_grants WHERE grant_id=$1 ORDER BY user_id`, item.ID)
			if err != nil {
				return err
			}
			for memberRows.Next() {
				var id string
				if err := memberRows.Scan(&id); err != nil {
					memberRows.Close()
					return err
				}
				item.MemberUserIDs = append(item.MemberUserIDs, id)
			}
			if err := memberRows.Close(); err != nil {
				return err
			}
		}
		return nil
	})
	return items, err
}
