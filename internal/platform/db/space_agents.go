package db

import (
	"context"
	"database/sql"
	"strings"
	"time"
	"unicode/utf8"
)

// SpaceAgent lists an agent in a Space, under the person who owns it. The agent
// lives on that person's computer: the Space keeps its id and name and nothing
// else, and only its owner can ask it to act.
type SpaceAgent struct {
	SpaceID     string    `json:"space_id"`
	SpaceName   string    `json:"space_name"`
	AgentID     string    `json:"agent_id"`
	AgentName   string    `json:"agent_name"`
	OwnerUserID string    `json:"owner_user_id"`
	OwnerName   string    `json:"owner_name"`
	AddedAt     time.Time `json:"added_at"`
	// Visibility is "public" or "private": a private agent is invisible to the
	// rest of the Space and can't be @mentioned there.
	Visibility string `json:"visibility"`
}

const spaceAgentColumns = `m.space_id,s.name,m.agent_id,m.agent_name,m.owner_user_id,COALESCE(u.username,''),m.added_at,m.visibility`

// Only agents in active Spaces are visible. The owner is always a member:
// a listing cascades from space_members.
const spaceAgentJoins = ` FROM space_agents m
	JOIN spaces s ON s.id=m.space_id AND s.lifecycle_state='active'
	LEFT JOIN users u ON u.id=m.owner_user_id`

func scanSpaceAgent(row interface{ Scan(...any) error }, out *SpaceAgent) error {
	return row.Scan(&out.SpaceID, &out.SpaceName, &out.AgentID, &out.AgentName, &out.OwnerUserID, &out.OwnerName, &out.AddedAt, &out.Visibility)
}

const (
	maxSpaceAgentIDLength   = 200
	maxSpaceAgentNameLength = 80
)

// AddSpaceAgent lists one of the caller's agents in a Space they belong to, or
// updates its name if it's already there. The id and name come from the
// caller's computer and mean nothing to the server beyond this listing.
func (db *Database) AddSpaceAgent(ctx context.Context, userID, spaceID, agentID, name string) (*SpaceAgent, error) {
	agentID, name = strings.TrimSpace(agentID), strings.TrimSpace(name)
	if agentID == "" || len(agentID) > maxSpaceAgentIDLength || name == "" || utf8.RuneCountInString(name) > maxSpaceAgentNameLength {
		return nil, ErrSpaceInvalid
	}
	out := &SpaceAgent{}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO space_agents(space_id,agent_id,owner_user_id,agent_name) VALUES($1,$2,$3,$4)
			ON CONFLICT (space_id,owner_user_id,agent_id) DO UPDATE SET agent_name=EXCLUDED.agent_name`, spaceID, agentID, userID, name); err != nil {
			return err
		}
		if err := scanSpaceAgent(tx.QueryRowContext(ctx, `SELECT `+spaceAgentColumns+spaceAgentJoins+` WHERE m.space_id=$1 AND m.agent_id=$2 AND m.owner_user_id=$3`, spaceID, agentID, userID), out); err != nil {
			return err
		}
		_, err := recordSpaceEventTx(ctx, tx, spaceID, userID, "agent.added", agentID, map[string]any{"agent": out})
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// RemoveSpaceAgent takes an agent out of a Space. Its owner or the Space's
// owner may do it.
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
			ORDER BY s.name,m.agent_name,m.agent_id LIMIT 500`, userID, spaceID)
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

func agentInSpaceTx(ctx context.Context, tx *sql.Tx, ownerUserID, spaceID, agentID string) (bool, error) {
	var in bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM space_agents WHERE space_id=$1 AND agent_id=$2 AND owner_user_id=$3)`, spaceID, agentID, ownerUserID).Scan(&in)
	return in, err
}
