package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// SpaceAgent is an agent's membership in a Space, under the person who owns
// it. Anyone in the Space can ask it for work.
type SpaceAgent struct {
	SpaceID       string          `json:"space_id"`
	SpaceName     string          `json:"space_name"`
	AgentID       string          `json:"agent_id"`
	AgentName     string          `json:"agent_name"`
	AgentRole     string          `json:"agent_role"`
	AgentAvatar   json.RawMessage `json:"agent_avatar"`
	SystemManaged bool            `json:"system_managed"`
	OwnerUserID   string          `json:"owner_user_id"`
	OwnerName     string          `json:"owner_name"`
	AddedAt       time.Time       `json:"added_at"`
	// Visibility is "public" or "private": a private agent is invisible to the
	// rest of the Space and can't be @mentioned there.
	Visibility string `json:"visibility"`
}

var (
	ErrAgentNotInSpace     = errors.New("agent is not in this Space")
	ErrAgentRequestBusy    = errors.New("agent has too many open requests")
	ErrAgentRequestChained = errors.New("work requested by another member cannot request more work")
	ErrAgentRequestLimit   = errors.New("agent request limit reached")
)

const spaceAgentColumns = `m.space_id,s.name,m.agent_id,a.name,a.role,a.avatar,a.system_managed,m.owner_user_id,COALESCE(u.username,''),m.added_at,m.visibility`

// Only enabled agents in active Spaces are visible or reachable. The owner is
// always a member: membership cascades from space_members.
const spaceAgentJoins = ` FROM space_agents m
	JOIN spaces s ON s.id=m.space_id AND s.lifecycle_state='active'
	JOIN misty_ask_identities a ON a.id=m.agent_id AND a.owner_user_id=m.owner_user_id AND a.enabled AND a.deleted_at IS NULL
	LEFT JOIN users u ON u.id=m.owner_user_id`

func scanSpaceAgent(row interface{ Scan(...any) error }, out *SpaceAgent) error {
	return row.Scan(&out.SpaceID, &out.SpaceName, &out.AgentID, &out.AgentName, &out.AgentRole, &out.AgentAvatar, &out.SystemManaged, &out.OwnerUserID, &out.OwnerName, &out.AddedAt, &out.Visibility)
}

// AddSpaceAgent brings one of the caller's agents into a Space they belong to.
func (db *Database) AddSpaceAgent(ctx context.Context, userID, spaceID, agentID string) (*SpaceAgent, error) {
	out := &SpaceAgent{}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		var owned bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM misty_ask_identities WHERE id=$1 AND owner_user_id=$2 AND deleted_at IS NULL)`, agentID, userID).Scan(&owned); err != nil {
			return err
		}
		if !owned {
			return ErrPersonalAgentNotFound
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO space_agents(space_id,agent_id,owner_user_id) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, spaceID, agentID, userID); err != nil {
			return err
		}
		if err := scanSpaceAgent(tx.QueryRowContext(ctx, `SELECT `+spaceAgentColumns+spaceAgentJoins+` WHERE m.space_id=$1 AND m.agent_id=$2`, spaceID, agentID), out); err != nil {
			return err
		}
		_, err := recordSpaceEventTx(ctx, tx, spaceID, userID, "agent.added", agentID, map[string]any{"agent": out})
		return err
	})
	return out, err
}

// RemoveSpaceAgent takes an agent out of a Space. Its owner or the Space's
// owner may do it. Open requests stop at their next tool call, because every
// call re-checks membership.
func (db *Database) RemoveSpaceAgent(ctx context.Context, userID, spaceID, agentID string) error {
	return db.SpaceTx(ctx, func(tx *sql.Tx) error {
		role, err := requireSpaceMemberTx(ctx, tx, spaceID, userID)
		if err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `DELETE FROM space_agents WHERE space_id=$1 AND agent_id=$2 AND (owner_user_id=$3 OR ($4 AND visibility='public'))`, spaceID, agentID, userID, role == "owner")
		if err != nil {
			return err
		}
		if n, err := result.RowsAffected(); err != nil || n == 0 {
			if err == nil {
				err = ErrSpaceNotFound
			}
			return err
		}
		_, err = recordSpaceEventTx(ctx, tx, spaceID, userID, "agent.removed", agentID, map[string]any{"agent_id": agentID})
		return err
	})
}

// SpaceAgents lists the agents in one Space, or in every Space the caller
// belongs to when spaceID is empty.
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
			ORDER BY s.name,a.name,m.agent_id LIMIT 500`, userID, spaceID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item SpaceAgent
			if err := scanSpaceAgent(rows, &item); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}

// AgentSpaceIDs lists the Spaces one of the caller's agents is in.
func (db *Database) AgentSpaceIDs(ctx context.Context, userID, agentID string) ([]string, error) {
	ids := []string{}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT m.space_id FROM space_agents m JOIN spaces s ON s.id=m.space_id AND s.lifecycle_state='active'
			WHERE m.agent_id=$1 AND m.owner_user_id=$2 ORDER BY s.is_default DESC,s.name,m.space_id`, agentID, userID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return err
			}
			ids = append(ids, id)
		}
		return rows.Err()
	})
	return ids, err
}

func agentInSpaceTx(ctx context.Context, tx *sql.Tx, ownerUserID, spaceID, agentID string) (bool, error) {
	var in bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM space_agents WHERE space_id=$1 AND agent_id=$2 AND owner_user_id=$3)`, spaceID, agentID, ownerUserID).Scan(&in)
	return in, err
}
