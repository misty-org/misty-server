package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
)

// maxGroupDirectMembers is the most people a group DM holds, the one who made it included.
const maxGroupDirectMembers = 10

// loadConversationAgentParticipantsTx adds the agents a conversation lists to its participants, after the
// people. An agent is its owner's: only they can ask it to act.
func loadConversationAgentParticipantsTx(ctx context.Context, tx *sql.Tx, conversation *SpaceConversation) error {
	rows, err := tx.QueryContext(ctx, `SELECT a.agent_id,a.owner_user_id,a.agent_name,a.added_at
		FROM space_conversation_agents a WHERE a.conversation_id=$1 ORDER BY a.agent_name,a.agent_id`, conversation.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		participant := SpaceConversationParticipant{SpaceActorRef: SpaceActorRef{Kind: "agent"}}
		if err := rows.Scan(&participant.AgentID, &participant.OwnerUserID, &participant.Name, &participant.JoinedAt); err != nil {
			return err
		}
		conversation.Participants = append(conversation.Participants, participant)
	}
	return rows.Err()
}

// groupDirectTitle names a group DM by its members, cut to the title limit.
func groupDirectTitle(names []string) string {
	title := strings.Join(names, ", ")
	if runes := []rune(title); len(runes) > 80 {
		title = string(runes[:79]) + "…"
	}
	return title
}

// CreateGroupDirectConversation starts a DM with two or more other people. Unlike a one-to-one DM it is
// never reused: the same people can have several. Only its members ever see it.
func (db *Database) CreateGroupDirectConversation(ctx context.Context, userID, spaceID string, otherUserIDs []string) (*SpaceConversation, error) {
	members := []string{strings.TrimSpace(userID)}
	seen := map[string]bool{members[0]: true}
	for _, id := range otherUserIDs {
		if id = strings.TrimSpace(id); id != "" && !seen[id] {
			seen[id] = true
			members = append(members, id)
		}
	}
	if members[0] == "" || len(members) < 3 || len(members) > maxGroupDirectMembers {
		return nil, ErrSpaceInvalid
	}
	out := &SpaceConversation{}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpaceMessageWriteTx(ctx, tx, userID, spaceID); err != nil {
			return err
		}
		names, err := spaceMemberNamesTx(ctx, tx, spaceID, members)
		if err != nil {
			return err
		}
		others := make([]string, 0, len(members)-1)
		for _, id := range members[1:] {
			others = append(others, names[id])
		}
		out.ID = "space_conversation_" + uuid.NewString()
		out.SpaceID, out.Kind, out.CreatedByUserID = spaceID, "dm", userID
		out.Title = groupDirectTitle(append([]string{names[userID]}, others...))
		if err := tx.QueryRowContext(ctx, `INSERT INTO space_conversations(id,space_id,title,created_by_user_id,kind)
			VALUES($1,$2,$3,$4,'dm') RETURNING created_at,updated_at`, out.ID, spaceID, out.Title, userID).Scan(&out.CreatedAt, &out.UpdatedAt); err != nil {
			return err
		}
		participants := make([]SpaceActorRef, 0, len(members))
		for _, memberID := range members {
			if _, err := tx.ExecContext(ctx, `INSERT INTO space_conversation_members(conversation_id,user_id) VALUES($1,$2)`, out.ID, memberID); err != nil {
				return err
			}
			participants = append(participants, SpaceActorRef{Kind: "person", UserID: memberID})
		}
		if _, err := recordSpaceEventTx(ctx, tx, spaceID, userID, "conversation.created", out.ID, map[string]any{"conversation_id": out.ID, "title": out.Title, "participants": participants}); err != nil {
			return err
		}
		return fillNewDirectConversationTx(ctx, tx, userID, spaceID, out)
	})
	return out, err
}

func fillNewDirectConversationTx(ctx context.Context, tx *sql.Tx, userID, spaceID string, out *SpaceConversation) error {
	out.VisibleToSpace = false
	if err := loadSpaceConversationParticipantsTx(ctx, tx, out); err != nil {
		return err
	}
	access, err := memberAccessTx(ctx, tx, spaceID, userID)
	if err != nil {
		return err
	}
	return fillConversationAccessTx(ctx, tx, access, out)
}

// spaceMemberNamesTx maps each id to its member's name, and fails when any is not in the Space.
func spaceMemberNamesTx(ctx context.Context, tx *sql.Tx, spaceID string, ids []string) (map[string]string, error) {
	names := map[string]string{}
	for _, id := range ids {
		var name string
		err := tx.QueryRowContext(ctx, `SELECT u.username FROM space_members sm JOIN users u ON u.id=sm.user_id WHERE sm.space_id=$1 AND sm.user_id=$2`, spaceID, id).Scan(&name)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && strings.TrimSpace(name) == "") {
			return nil, ErrSpaceInvalid
		}
		if err != nil {
			return nil, err
		}
		names[id] = name
	}
	return names, nil
}

// directConversationSizeTx is a DM's kind and how many people are in it.
func directConversationSizeTx(ctx context.Context, tx *sql.Tx, spaceID, conversationID string) (kind string, people int, err error) {
	err = tx.QueryRowContext(ctx, `SELECT c.kind,(SELECT count(*) FROM space_conversation_members cm WHERE cm.conversation_id=c.id AND cm.actor_kind='person')
		FROM space_conversations c WHERE c.id=$1 AND c.space_id=$2`, conversationID, spaceID).Scan(&kind, &people)
	if errors.Is(err, sql.ErrNoRows) {
		return "", 0, ErrSpaceNotFound
	}
	return kind, people, err
}

// AddGroupDirectMembers adds people to a group DM that has three or more already; a one-to-one DM
// never grows (a new group is made instead). Any member may add. They see the whole history.
func (db *Database) AddGroupDirectMembers(ctx context.Context, userID, spaceID, conversationID string, userIDs []string) (*SpaceConversation, error) {
	out := &SpaceConversation{}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpaceConversationMemberTx(ctx, tx, userID, spaceID, conversationID); err != nil {
			return err
		}
		kind, people, err := directConversationSizeTx(ctx, tx, spaceID, conversationID)
		if err != nil {
			return err
		}
		if kind != "dm" || people < 3 {
			return ErrSpaceInvalid
		}
		added := []string{}
		for _, id := range userIDs {
			id = strings.TrimSpace(id)
			if id == "" {
				continue
			}
			var already bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM space_conversation_members WHERE conversation_id=$1 AND user_id=$2)`, conversationID, id).Scan(&already); err != nil {
				return err
			}
			if already {
				continue
			}
			if _, err := spaceMemberNamesTx(ctx, tx, spaceID, []string{id}); err != nil {
				return err
			}
			added = append(added, id)
		}
		if len(added) == 0 || people+len(added) > maxGroupDirectMembers {
			return ErrSpaceInvalid
		}
		for _, id := range added {
			if _, err := tx.ExecContext(ctx, `INSERT INTO space_conversation_members(conversation_id,user_id) VALUES($1,$2)`, conversationID, id); err != nil {
				return err
			}
		}
		if _, err := recordSpaceEventTx(ctx, tx, spaceID, userID, "conversation.updated", conversationID, map[string]any{"conversation_id": conversationID, "members_changed": true}); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `SELECT id,space_id,title,kind,created_by_user_id,created_at,updated_at FROM space_conversations WHERE id=$1`, conversationID).
			Scan(&out.ID, &out.SpaceID, &out.Title, &out.Kind, &out.CreatedByUserID, &out.CreatedAt, &out.UpdatedAt); err != nil {
			return err
		}
		return fillNewDirectConversationTx(ctx, tx, userID, spaceID, out)
	})
	return out, err
}

// LeaveGroupDirectConversation takes the caller, and their agents, out of a group DM that keeps at least
// two people. Their messages stay. Everyone else is told the members changed; the leaver is told it is gone.
func (db *Database) LeaveGroupDirectConversation(ctx context.Context, userID, spaceID, conversationID string) error {
	return db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpaceConversationMemberTx(ctx, tx, userID, spaceID, conversationID); err != nil {
			return err
		}
		kind, people, err := directConversationSizeTx(ctx, tx, spaceID, conversationID)
		if err != nil {
			return err
		}
		if kind != "dm" || people < 3 {
			return ErrSpaceInvalid
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM space_conversation_agents WHERE conversation_id=$1 AND owner_user_id=$2`, conversationID, userID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM space_conversation_members WHERE conversation_id=$1 AND user_id=$2`, conversationID, userID); err != nil {
			return err
		}
		if _, err := recordSpaceEventTx(ctx, tx, spaceID, userID, "conversation.updated", conversationID, map[string]any{"conversation_id": conversationID, "members_changed": true}); err != nil {
			return err
		}
		_, err = recordSpaceEventTx(ctx, tx, spaceID, userID, "conversation.removed", conversationID, map[string]any{"conversation_id": conversationID, "user_id": userID})
		return err
	})
}

// AddConversationAgent lists one of the caller's agents in a DM they are in. The id and name come from
// their computer and mean nothing to the server beyond this listing.
func (db *Database) AddConversationAgent(ctx context.Context, userID, spaceID, conversationID, agentID, name string) error {
	agentID, name = strings.TrimSpace(agentID), strings.TrimSpace(name)
	if agentID == "" || len(agentID) > maxSpaceAgentIDLength || name == "" || utf8.RuneCountInString(name) > maxSpaceAgentNameLength {
		return ErrSpaceInvalid
	}
	return db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpaceConversationMemberTx(ctx, tx, userID, spaceID, conversationID); err != nil {
			return err
		}
		kind, _, err := directConversationSizeTx(ctx, tx, spaceID, conversationID)
		if err != nil {
			return err
		}
		if kind != "dm" {
			return ErrSpaceInvalid
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO space_conversation_agents(conversation_id,agent_id,owner_user_id,agent_name) VALUES($1,$2,$3,$4)
			ON CONFLICT (conversation_id,owner_user_id,agent_id) DO UPDATE SET agent_name=EXCLUDED.agent_name`, conversationID, agentID, userID, name); err != nil {
			return err
		}
		_, err = recordSpaceEventTx(ctx, tx, spaceID, userID, "conversation.updated", conversationID, map[string]any{"conversation_id": conversationID, "members_changed": true})
		return err
	})
}

// RemoveConversationAgent takes the caller's agent out of a DM.
func (db *Database) RemoveConversationAgent(ctx context.Context, userID, spaceID, conversationID, agentID string) error {
	return db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpaceConversationMemberTx(ctx, tx, userID, spaceID, conversationID); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `DELETE FROM space_conversation_agents WHERE conversation_id=$1 AND agent_id=$2 AND owner_user_id=$3`, conversationID, agentID, userID)
		if err != nil {
			return err
		}
		if n, err := result.RowsAffected(); err != nil || n == 0 {
			if err == nil {
				err = ErrSpaceNotFound
			}
			return err
		}
		_, err = recordSpaceEventTx(ctx, tx, spaceID, userID, "conversation.updated", conversationID, map[string]any{"conversation_id": conversationID, "members_changed": true})
		return err
	})
}

// requireConversationAgentMentionTx decides whether the sender may @mention an agent in a conversation.
// In a group DM (three or more people) only the sender's own agent that the group lists counts: owner-only.
// Anywhere else the Space's own listing decides, as before.
func requireConversationAgentMentionTx(ctx context.Context, tx *sql.Tx, userID, spaceID, conversationID, agentID string) error {
	if conversationID != "" {
		kind, people, err := directConversationSizeTx(ctx, tx, spaceID, conversationID)
		if err != nil {
			return err
		}
		if kind == "dm" && people >= 3 {
			var listed bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM space_conversation_agents WHERE conversation_id=$1 AND agent_id=$2 AND owner_user_id=$3)`, conversationID, agentID, userID).Scan(&listed); err != nil || !listed {
				return ErrSpaceInvalid
			}
			return nil
		}
	}
	var listed bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM space_agents WHERE space_id=$1 AND agent_id=$2 AND (visibility='public' OR owner_user_id=$3))`, spaceID, agentID, userID).Scan(&listed); err != nil || !listed {
		return ErrSpaceInvalid
	}
	return nil
}
