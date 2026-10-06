package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// SpaceAgentListing is one member's agent published to a Space. Other members'
// agents can send it requests while the listing accepts them.
type SpaceAgentListing struct {
	SpaceID         string    `json:"space_id"`
	SpaceName       string    `json:"space_name"`
	AgentID         string    `json:"agent_id"`
	AgentName       string    `json:"agent_name"`
	AgentRole       string    `json:"agent_role"`
	OwnerUserID     string    `json:"owner_user_id"`
	OwnerName       string    `json:"owner_name"`
	Description     string    `json:"description"`
	AcceptPolicy    string    `json:"accept_policy"`
	MaxOpenRequests int       `json:"max_open_requests"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type SpaceAgentListingInput struct {
	Description     string `json:"description"`
	AcceptPolicy    string `json:"accept_policy"`
	MaxOpenRequests int    `json:"max_open_requests"`
}

var (
	ErrAgentListingUnavailable = errors.New("agent is not available to this Space")
	ErrAgentRequestBusy        = errors.New("agent has too many open requests")
	ErrAgentRequestChained     = errors.New("work requested by another member cannot request more work")
	ErrAgentRequestLimit       = errors.New("agent request limit reached")
)

const spaceAgentListingColumns = `l.space_id,s.name,l.agent_id,a.name,a.role,l.owner_user_id,COALESCE(u.name,''),l.description,l.accept_policy,l.max_open_requests,l.updated_at`

// Only listings whose agent is enabled and whose owner is still a member of an
// active Space are visible or reachable.
const spaceAgentListingJoins = ` FROM space_agent_listings l
	JOIN spaces s ON s.id=l.space_id AND s.lifecycle_state='active'
	JOIN space_members owner_member ON owner_member.space_id=l.space_id AND owner_member.user_id=l.owner_user_id
	JOIN misty_ask_identities a ON a.id=l.agent_id AND a.owner_user_id=l.owner_user_id AND a.enabled AND a.deleted_at IS NULL
	LEFT JOIN users u ON u.id=l.owner_user_id`

func scanSpaceAgentListing(row interface{ Scan(...any) error }, out *SpaceAgentListing) error {
	return row.Scan(&out.SpaceID, &out.SpaceName, &out.AgentID, &out.AgentName, &out.AgentRole, &out.OwnerUserID, &out.OwnerName, &out.Description, &out.AcceptPolicy, &out.MaxOpenRequests, &out.UpdatedAt)
}

// SaveSpaceAgentListing publishes the caller's own agent to a Space they
// belong to, or updates how it accepts requests.
func (db *Database) SaveSpaceAgentListing(ctx context.Context, userID, spaceID, agentID string, input SpaceAgentListingInput) (*SpaceAgentListing, error) {
	input.Description = strings.TrimSpace(input.Description)
	input.AcceptPolicy = strings.TrimSpace(input.AcceptPolicy)
	if input.AcceptPolicy == "" {
		input.AcceptPolicy = "ask"
	}
	if input.MaxOpenRequests == 0 {
		input.MaxOpenRequests = 3
	}
	if len([]rune(input.Description)) > 500 || (input.AcceptPolicy != "ask" && input.AcceptPolicy != "auto" && input.AcceptPolicy != "off") || input.MaxOpenRequests < 1 || input.MaxOpenRequests > 10 {
		return nil, ErrSpaceInvalid
	}
	out := &SpaceAgentListing{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		var owned bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM misty_ask_identities WHERE id=$1 AND owner_user_id=$2 AND enabled AND deleted_at IS NULL)`, agentID, userID).Scan(&owned); err != nil {
			return err
		}
		if !owned {
			return ErrPersonalAgentNotFound
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO space_agent_listings(space_id,agent_id,owner_user_id,description,accept_policy,max_open_requests)
			VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(space_id,agent_id) DO UPDATE SET description=EXCLUDED.description,
			accept_policy=EXCLUDED.accept_policy,max_open_requests=EXCLUDED.max_open_requests,updated_at=NOW()`,
			spaceID, agentID, userID, input.Description, input.AcceptPolicy, input.MaxOpenRequests); err != nil {
			return err
		}
		if err := scanSpaceAgentListing(tx.QueryRowContext(ctx, `SELECT `+spaceAgentListingColumns+spaceAgentListingJoins+` WHERE l.space_id=$1 AND l.agent_id=$2`, spaceID, agentID), out); err != nil {
			return err
		}
		_, err := recordSpaceEventTx(ctx, tx, spaceID, userID, "agent.listing.updated", agentID, map[string]any{"listing": out})
		return err
	})
	return out, err
}

// DeleteSpaceAgentListing unpublishes the caller's agent. Open requests stop
// at their next tool call because every call re-checks the listing.
func (db *Database) DeleteSpaceAgentListing(ctx context.Context, userID, spaceID, agentID string) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `DELETE FROM space_agent_listings WHERE space_id=$1 AND agent_id=$2 AND owner_user_id=$3`, spaceID, agentID, userID)
		if err != nil {
			return err
		}
		if n, err := result.RowsAffected(); err != nil || n == 0 {
			if err == nil {
				err = ErrSpaceNotFound
			}
			return err
		}
		_, err = recordSpaceEventTx(ctx, tx, spaceID, userID, "agent.listing.deleted", agentID, map[string]any{"agent_id": agentID})
		return err
	})
}

// SpaceAgentListings lists agents published to one Space, or to every Space
// the caller belongs to when spaceID is empty.
func (db *Database) SpaceAgentListings(ctx context.Context, userID, spaceID string) ([]SpaceAgentListing, error) {
	items := []SpaceAgentListing{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if spaceID != "" {
			if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
				return err
			}
		}
		rows, err := tx.QueryContext(ctx, `SELECT `+spaceAgentListingColumns+spaceAgentListingJoins+`
			JOIN space_members viewer ON viewer.space_id=l.space_id AND viewer.user_id=$1
			WHERE ($2='' OR l.space_id=$2) ORDER BY s.name,a.name,l.agent_id LIMIT 200`, userID, spaceID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item SpaceAgentListing
			if err := scanSpaceAgentListing(rows, &item); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}
