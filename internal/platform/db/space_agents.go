package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

type SpaceAgent struct {
	SpaceID         string          `json:"space_id"`
	SpaceName       string          `json:"space_name"`
	AgentID         string          `json:"agent_id"`
	AgentName       string          `json:"agent_name"`
	OwnerUserID     string          `json:"owner_user_id"`
	OwnerName       string          `json:"owner_name"`
	AddedAt         time.Time       `json:"added_at"`
	Visibility      string          `json:"visibility"`
	Role            string          `json:"role"`
	Description     string          `json:"description"`
	Instructions    string          `json:"instructions"`
	Soul            string          `json:"soul"`
	Icon            string          `json:"icon"`
	Avatar          json.RawMessage `json:"avatar"`
	VoiceID         string          `json:"voice_id"`
	EngineID        string          `json:"engine_id"`
	EngineModel     string          `json:"engine_model"`
	ReasoningEffort string          `json:"reasoning_effort"`
	Origin          string          `json:"origin"`
	Revision        int64           `json:"revision"`
	UpdatedAt       time.Time       `json:"updated_at"`
	UpdatedByUserID string          `json:"updated_by_user_id,omitempty"`
	CanEdit         bool            `json:"can_edit"`
}

type SpaceAgentDefinition struct {
	Name            string          `json:"name"`
	Role            string          `json:"role"`
	Description     string          `json:"description"`
	Instructions    string          `json:"instructions"`
	Soul            string          `json:"soul"`
	Icon            string          `json:"icon"`
	Avatar          json.RawMessage `json:"avatar,omitempty"`
	VoiceID         string          `json:"voice_id"`
	EngineID        string          `json:"engine_id"`
	EngineModel     string          `json:"engine_model"`
	ReasoningEffort string          `json:"reasoning_effort"`
	Origin          string          `json:"origin"`
	Revision        int64           `json:"revision"`
}

const spaceAgentColumns = `m.space_id,s.name,m.agent_id,m.agent_name,m.owner_user_id,COALESCE(u.username,''),m.added_at,m.visibility,
	m.role,m.description,m.instructions,m.soul,m.icon,m.avatar,m.voice_id,m.engine_id,m.engine_model,m.reasoning_effort,m.origin,
	m.revision,m.updated_at,COALESCE(m.updated_by_user_id,'')`

const spaceAgentJoins = ` FROM space_agents m
	JOIN spaces s ON s.id=m.space_id AND s.lifecycle_state='active'
	LEFT JOIN users u ON u.id=m.owner_user_id`

func scanSpaceAgent(row interface{ Scan(...any) error }, out *SpaceAgent) error {
	var avatar []byte
	if err := row.Scan(&out.SpaceID, &out.SpaceName, &out.AgentID, &out.AgentName, &out.OwnerUserID, &out.OwnerName, &out.AddedAt, &out.Visibility,
		&out.Role, &out.Description, &out.Instructions, &out.Soul, &out.Icon, &avatar, &out.VoiceID, &out.EngineID, &out.EngineModel,
		&out.ReasoningEffort, &out.Origin, &out.Revision, &out.UpdatedAt, &out.UpdatedByUserID); err != nil {
		return err
	}
	out.Avatar = json.RawMessage(avatar)
	return nil
}

const (
	maxSpaceAgentIDLength           = 200
	maxSpaceAgentNameLength         = 80
	maxSpaceAgentRoleLength         = 160
	maxSpaceAgentDescriptionLength  = 2000
	maxSpaceAgentInstructionsLength = 16000
	maxSpaceAgentSoulLength         = 100000
	maxSpaceAgentIconLength         = 128
	maxSpaceAgentAvatarBytes        = 4096
	maxSpaceAgentModelLength        = 200
	maxSpaceAgentEffortLength       = 32
	maxSpaceAgentOriginLength       = 200
	MaxSpaceAgentBodyBytes          = 1 << 20
)

var (
	spaceAgentVoicePattern  = regexp.MustCompile(`^[a-z0-9-]{0,64}$`)
	spaceAgentEnginePattern = regexp.MustCompile(`^[a-z0-9_-]{0,40}$`)
)

func runesAtMost(value string, most int) bool { return utf8.RuneCountInString(value) <= most }

func normalizeSpaceAgentDefinition(def SpaceAgentDefinition) (SpaceAgentDefinition, error) {
	def.Name = strings.TrimSpace(def.Name)
	def.Role = strings.TrimSpace(def.Role)
	def.VoiceID = strings.TrimSpace(def.VoiceID)
	def.EngineID = strings.TrimSpace(def.EngineID)
	def.EngineModel = strings.TrimSpace(def.EngineModel)
	def.ReasoningEffort = strings.TrimSpace(def.ReasoningEffort)
	def.Origin = strings.TrimSpace(def.Origin)
	if def.Name == "" || !runesAtMost(def.Name, maxSpaceAgentNameLength) ||
		!runesAtMost(def.Role, maxSpaceAgentRoleLength) ||
		!runesAtMost(def.Description, maxSpaceAgentDescriptionLength) ||
		!runesAtMost(def.Instructions, maxSpaceAgentInstructionsLength) ||
		!runesAtMost(def.Soul, maxSpaceAgentSoulLength) ||
		!runesAtMost(def.Icon, maxSpaceAgentIconLength) ||
		!runesAtMost(def.EngineModel, maxSpaceAgentModelLength) ||
		!runesAtMost(def.ReasoningEffort, maxSpaceAgentEffortLength) ||
		!runesAtMost(def.Origin, maxSpaceAgentOriginLength) ||
		!spaceAgentVoicePattern.MatchString(def.VoiceID) ||
		!spaceAgentEnginePattern.MatchString(def.EngineID) ||
		def.Revision < 0 {
		return def, ErrSpaceInvalid
	}
	if len(def.Avatar) == 0 || string(def.Avatar) == "null" {
		def.Avatar = json.RawMessage(`{}`)
	}
	if len(def.Avatar) > maxSpaceAgentAvatarBytes {
		return def, ErrSpaceInvalid
	}
	var avatar map[string]json.RawMessage
	if err := json.Unmarshal(def.Avatar, &avatar); err != nil || avatar == nil {
		return def, ErrSpaceInvalid
	}
	for _, value := range avatar {
		trimmed := strings.TrimSpace(string(value))
		if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
			return def, ErrSpaceInvalid
		}
	}
	return def, nil
}

func canEditSpaceAgent(access *memberAccess, ownerUserID, userID string) bool {
	return ownerUserID == userID || access.owner || access.base.Has(PermissionMessagesManage)
}

func spaceAgentEventPayload(agent *SpaceAgent) map[string]any {
	return map[string]any{
		"agent_id":      agent.AgentID,
		"revision":      agent.Revision,
		"owner_user_id": agent.OwnerUserID,
		"visibility":    agent.Visibility,
	}
}

func (db *Database) PutSpaceAgent(ctx context.Context, userID, spaceID, agentID string, def SpaceAgentDefinition) (*SpaceAgent, error) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" || len(agentID) > maxSpaceAgentIDLength {
		return nil, ErrSpaceInvalid
	}
	def, err := normalizeSpaceAgentDefinition(def)
	if err != nil {
		return nil, err
	}
	out := &SpaceAgent{}
	err = db.SpaceTx(ctx, func(tx *sql.Tx) error {
		access, err := memberAccessTx(ctx, tx, spaceID, userID)
		if err != nil {
			return err
		}
		var ownerID, visibility string
		var revision int64
		err = tx.QueryRowContext(ctx, `SELECT owner_user_id,visibility,revision FROM space_agents WHERE space_id=$1 AND agent_id=$2 FOR UPDATE`, spaceID, agentID).Scan(&ownerID, &visibility, &revision)
		event := "agent.updated"
		switch {
		case errors.Is(err, sql.ErrNoRows):
			if def.Revision != 0 {
				return ErrSpaceNotFound
			}
			if err := requireSpaceAgentRoomTx(ctx, tx, spaceID, agentID, userID); err != nil {
				return err
			}
			result, err := tx.ExecContext(ctx, `INSERT INTO space_agents(space_id,agent_id,owner_user_id,agent_name,role,description,instructions,soul,icon,avatar,
				voice_id,engine_id,engine_model,reasoning_effort,origin,revision,updated_at,updated_by_user_id)
				VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,1,NOW(),$3)
				ON CONFLICT (space_id,agent_id) DO NOTHING`,
				spaceID, agentID, userID, def.Name, def.Role, def.Description, def.Instructions, def.Soul, def.Icon, string(def.Avatar),
				def.VoiceID, def.EngineID, def.EngineModel, def.ReasoningEffort, def.Origin)
			if err != nil {
				return err
			}
			if added, err := result.RowsAffected(); err != nil || added == 0 {
				if err == nil {
					err = ErrSpaceConflict
				}
				return err
			}
			event = "agent.added"
		case err != nil:
			return err
		default:
			if visibility == VisibilityPrivate && ownerID != userID {
				return ErrSpaceNotFound
			}
			if revision == 0 && ownerID != userID {
				return ErrSpaceConflict
			}
			if !canEditSpaceAgent(access, ownerID, userID) {
				return ErrSpaceForbidden
			}
			if def.Revision != revision {
				return ErrSpaceConflict
			}
			if _, err := tx.ExecContext(ctx, `UPDATE space_agents SET agent_name=$3,role=$4,description=$5,instructions=$6,soul=$7,icon=$8,avatar=$9,
				voice_id=$10,engine_id=$11,engine_model=$12,reasoning_effort=$13,origin=$14,revision=revision+1,updated_at=NOW(),updated_by_user_id=$15
				WHERE space_id=$1 AND agent_id=$2`,
				spaceID, agentID, def.Name, def.Role, def.Description, def.Instructions, def.Soul, def.Icon, string(def.Avatar),
				def.VoiceID, def.EngineID, def.EngineModel, def.ReasoningEffort, def.Origin, userID); err != nil {
				return err
			}
		}
		if err := scanSpaceAgent(tx.QueryRowContext(ctx, `SELECT `+spaceAgentColumns+spaceAgentJoins+` WHERE m.space_id=$1 AND m.agent_id=$2`, spaceID, agentID), out); err != nil {
			return err
		}
		out.CanEdit = canEditSpaceAgent(access, out.OwnerUserID, userID)
		_, err = recordSpaceEventTx(ctx, tx, spaceID, userID, event, agentID, spaceAgentEventPayload(out))
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (db *Database) RemoveSpaceAgent(ctx context.Context, userID, spaceID, agentID string) error {
	return db.SpaceTx(ctx, func(tx *sql.Tx) error {
		access, err := memberAccessTx(ctx, tx, spaceID, userID)
		if err != nil {
			return err
		}
		var ownerID, visibility string
		err = tx.QueryRowContext(ctx, `SELECT owner_user_id,visibility FROM space_agents WHERE space_id=$1 AND agent_id=$2 FOR UPDATE`, spaceID, agentID).Scan(&ownerID, &visibility)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrSpaceNotFound
		}
		if err != nil {
			return err
		}
		if visibility == VisibilityPrivate && ownerID != userID {
			return ErrSpaceNotFound
		}
		if !canEditSpaceAgent(access, ownerID, userID) {
			return ErrSpaceNotFound
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM space_agents WHERE space_id=$1 AND agent_id=$2`, spaceID, agentID); err != nil {
			return err
		}
		_, err = recordSpaceEventTx(ctx, tx, spaceID, userID, "agent.removed", agentID, map[string]any{
			"agent_id": agentID, "owner_user_id": ownerID, "visibility": visibility,
		})
		return err
	})
}

const maxListedSpaceAgents = 2000

func (db *Database) SpaceAgents(ctx context.Context, userID, spaceID string) ([]SpaceAgent, error) {
	items := []SpaceAgent{}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if spaceID != "" {
			if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
				return err
			}
		}
		rows, err := tx.QueryContext(ctx, `SELECT `+spaceAgentColumns+spaceAgentJoins+`
			JOIN space_members viewer ON viewer.space_id=m.space_id AND viewer.user_id=$1
			WHERE ($2='' OR m.space_id=$2) AND (m.visibility='public' OR m.owner_user_id=$1)
			ORDER BY s.name,m.agent_name,m.agent_id LIMIT $3`, userID, spaceID, maxListedSpaceAgents)
		if err != nil {
			return err
		}
		for rows.Next() {
			var item SpaceAgent
			if err := scanSpaceAgent(rows, &item); err != nil {
				rows.Close()
				return err
			}
			items = append(items, item)
		}
		iterated := rows.Err()
		if err := rows.Close(); err != nil {
			return err
		}
		if iterated != nil {
			return iterated
		}
		accessBySpace := map[string]*memberAccess{}
		for index := range items {
			access, known := accessBySpace[items[index].SpaceID]
			if !known {
				access, err = memberAccessTx(ctx, tx, items[index].SpaceID, userID)
				if err != nil {
					return err
				}
				accessBySpace[items[index].SpaceID] = access
			}
			items[index].CanEdit = canEditSpaceAgent(access, items[index].OwnerUserID, userID)
		}
		return nil
	})
	return items, err
}

func agentInSpaceTx(ctx context.Context, tx *sql.Tx, ownerUserID, spaceID, agentID string) (bool, error) {
	var in bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM space_agents WHERE space_id=$1 AND agent_id=$2 AND owner_user_id=$3)`, spaceID, agentID, ownerUserID).Scan(&in)
	return in, err
}
