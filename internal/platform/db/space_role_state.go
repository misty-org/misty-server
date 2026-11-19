package db

import (
	"context"
	"database/sql"
	"sort"

	"github.com/lib/pq"
	"github.com/misty-org/misty-server/internal/spaces"
)

// lockSpaceRolesTx serializes changes to a Space's roles, their order, who
// holds them and channel overwrites, so positions and access diffs stay sound.
func lockSpaceRolesTx(ctx context.Context, tx *sql.Tx, spaceID string) error {
	_, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "spaces:roles:"+spaceID)
	return err
}

// ensureEveryoneRoleTx gives a Space its everyone role, with what members could
// do before roles existed, unless it has one. New Spaces get it when they're
// made; the migration gave every older Space one.
func ensureEveryoneRoleTx(ctx context.Context, tx *sql.Tx, spaceID string) error {
	if err := lockSpaceRolesTx(ctx, tx, spaceID); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO space_roles(space_id,name,position,permissions,is_default)
		SELECT $1::text,$2::text,0,$3::text[],TRUE WHERE NOT EXISTS(SELECT 1 FROM space_roles WHERE space_id=$1 AND is_default)`,
		spaceID, spaces.DefaultRoleName, pq.Array(spaces.DefaultEveryonePermissions()))
	return err
}

// spaceRoleState is a whole Space's roles, who holds them and its channels'
// overwrites, loaded at once to see who can read which channel.
type spaceRoleState struct {
	everyoneRoleID string
	roles          map[string]spaces.Role
	members        map[string]memberRoles
	channels       map[string][]spaces.Overwrite
}

type memberRoles struct {
	owner   bool
	roleIDs []string
}

func loadSpaceRoleStateTx(ctx context.Context, tx *sql.Tx, spaceID string) (*spaceRoleState, error) {
	state := &spaceRoleState{roles: map[string]spaces.Role{}, members: map[string]memberRoles{}}
	if err := state.loadRolesTx(ctx, tx, spaceID); err != nil {
		return nil, err
	}
	if err := state.loadMembersTx(ctx, tx, spaceID); err != nil {
		return nil, err
	}
	channels, err := spaceChannelOverwritesTx(ctx, tx, spaceID, true)
	if err != nil {
		return nil, err
	}
	state.channels = channels
	return state, nil
}

func (s *spaceRoleState) loadRolesTx(ctx context.Context, tx *sql.Tx, spaceID string) error {
	rows, err := tx.QueryContext(ctx, `SELECT id,position,permissions,is_default FROM space_roles WHERE space_id=$1`, spaceID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var role spaces.Role
		if err := rows.Scan(&role.ID, &role.Position, pq.Array(&role.Permissions), &role.IsDefault); err != nil {
			return err
		}
		if role.IsDefault {
			s.everyoneRoleID = role.ID
		}
		s.roles[role.ID] = role
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if s.everyoneRoleID == "" {
		s.roles[""] = spaces.Role{IsDefault: true, Permissions: spaces.DefaultEveryonePermissions()}
	}
	return nil
}

func (s *spaceRoleState) loadMembersTx(ctx context.Context, tx *sql.Tx, spaceID string) error {
	rows, err := tx.QueryContext(ctx, `SELECT m.user_id,m.role='owner',
		COALESCE(array_agg(mr.role_id::text) FILTER (WHERE mr.role_id IS NOT NULL),'{}'::text[])
		FROM space_members m
		LEFT JOIN space_member_roles mr ON mr.space_id=m.space_id AND mr.user_id=m.user_id
		WHERE m.space_id=$1 GROUP BY m.user_id,m.role`, spaceID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var userID string
		var member memberRoles
		if err := rows.Scan(&userID, &member.owner, pq.Array(&member.roleIDs)); err != nil {
			return err
		}
		s.members[userID] = member
	}
	return rows.Err()
}

// base is a member's Space-wide permissions, as memberAccessTx computes them.
func (s *spaceRoleState) base(userID string) spaces.PermissionSet {
	member, ok := s.members[userID]
	if !ok {
		return spaces.PermissionSet{}
	}
	if member.owner {
		return spaces.OwnerPermissions()
	}
	roles := []spaces.Role{s.roles[s.everyoneRoleID]}
	for _, roleID := range member.roleIDs {
		if role, ok := s.roles[roleID]; ok {
			roles = append(roles, role)
		}
	}
	return spaces.BasePermissions(roles)
}

// channelReadersTx is, for every channel the whole Space is in, the members who
// may read it.
func channelReadersTx(ctx context.Context, tx *sql.Tx, spaceID string) (map[string]map[string]bool, error) {
	state, err := loadSpaceRoleStateTx(ctx, tx, spaceID)
	if err != nil {
		return nil, err
	}
	bases := make(map[string]spaces.PermissionSet, len(state.members))
	for userID := range state.members {
		bases[userID] = state.base(userID)
	}
	out := make(map[string]map[string]bool, len(state.channels))
	for conversationID, overwrites := range state.channels {
		readers := map[string]bool{}
		for userID, member := range state.members {
			if spaces.ChannelPermissions(bases[userID], state.everyoneRoleID, member.roleIDs, overwrites).Has(PermissionMessagesRead) {
				readers[userID] = true
			}
		}
		out[conversationID] = readers
	}
	return out, nil
}

// announceChannelAccessTx compares who could read each channel before a change
// with who can now. Each member who lost a channel gets conversation.removed;
// a channel someone gained is announced again whole as conversation.updated,
// unless the caller announces that one itself (announced).
func announceChannelAccessTx(ctx context.Context, tx *sql.Tx, spaceID, actorID string, before map[string]map[string]bool, announced string) error {
	after, err := channelReadersTx(ctx, tx, spaceID)
	if err != nil {
		return err
	}
	conversationIDs := make([]string, 0, len(after))
	for conversationID := range after {
		conversationIDs = append(conversationIDs, conversationID)
	}
	sort.Strings(conversationIDs)
	for _, conversationID := range conversationIDs {
		readers, previous := after[conversationID], before[conversationID]
		for _, userID := range sortedKeys(previous) {
			if readers[userID] {
				continue
			}
			if _, err := recordSpaceEventTx(ctx, tx, spaceID, actorID, "conversation.removed", conversationID,
				map[string]any{"id": conversationID, "user_id": userID}); err != nil {
				return err
			}
		}
		gained := false
		for userID := range readers {
			gained = gained || !previous[userID]
		}
		if gained && conversationID != announced {
			if err := recordConversationUpdatedTx(ctx, tx, spaceID, actorID, conversationID); err != nil {
				return err
			}
		}
	}
	return nil
}

func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for key := range set {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}
