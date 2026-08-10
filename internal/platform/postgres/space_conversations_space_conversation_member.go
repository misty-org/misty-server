package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

type SpaceActorRef struct {
	Kind    string `json:"kind"`
	UserID  string `json:"user_id,omitempty"`
	AgentID string `json:"agent_id,omitempty"`
}

type SpaceConversationParticipant struct {
	SpaceActorRef
	Name     string          `json:"name"`
	Email    string          `json:"email,omitempty"`
	Avatar   json.RawMessage `json:"avatar,omitempty"`
	JoinedAt time.Time       `json:"joined_at"`
}

type SpaceConversation struct {
	ID                  string                         `json:"id"`
	SpaceID             string                         `json:"space_id"`
	Title               string                         `json:"title"`
	Kind                string                         `json:"kind"`
	CreatedByUserID     string                         `json:"created_by_user_id"`
	Origin              string                         `json:"origin"`
	IntegrationID       string                         `json:"integration_id,omitempty"`
	ExternalResourceID  string                         `json:"external_resource_id,omitempty"`
	ExternalDisplayName string                         `json:"external_display_name,omitempty"`
	IntegrationStatus   string                         `json:"integration_status"`
	VisibleToSpace      bool                           `json:"visible_to_space"`
	DirectUserID        string                         `json:"direct_user_id,omitempty"`
	DirectAgentID       string                         `json:"direct_agent_id,omitempty"`
	Participants        []SpaceConversationParticipant `json:"participants"`
	CreatedAt           time.Time                      `json:"created_at"`
	UpdatedAt           time.Time                      `json:"updated_at"`
}

func normalizeConversationTitle(title string) (string, error) {
	title = strings.TrimSpace(title)
	if title == "" || utf8.RuneCountInString(title) > 80 {
		return "", ErrSpaceInvalid
	}
	return title, nil
}

func requireSpaceConversationMemberTx(ctx context.Context, tx *sql.Tx, userID, spaceID, conversationID string) error {
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(
		SELECT 1 FROM space_conversations c
		JOIN space_members sm ON sm.space_id=c.space_id
		WHERE c.id=$1 AND c.space_id=$2 AND sm.user_id=$3
		  AND (c.visible_to_space OR EXISTS(
		      SELECT 1 FROM space_conversation_members cm
		      WHERE cm.conversation_id=c.id AND cm.user_id=$3
		  ))
	)`, conversationID, spaceID, userID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrSpaceForbidden
	}
	return nil
}

func loadSpaceConversationParticipantsTx(ctx context.Context, tx *sql.Tx, conversation *SpaceConversation) error {
	conversation.Participants = []SpaceConversationParticipant{}
	query := `SELECT cm.actor_kind,COALESCE(cm.user_id,''),COALESCE(cm.agent_id,''),
		CASE WHEN cm.actor_kind='person' THEN u.name ELSE COALESCE(v.name,'Former agent') END,
		CASE WHEN cm.actor_kind='person' THEN u.email ELSE '' END,
		CASE WHEN cm.actor_kind='agent' THEN v.avatar ELSE NULL END,cm.joined_at
		FROM space_conversation_members cm
		LEFT JOIN users u ON u.id=cm.user_id
		LEFT JOIN personal_agent_space_grants g ON g.agent_id=cm.agent_id AND g.space_id=$2
		LEFT JOIN personal_agent_versions v ON v.id=g.approved_version_id
		WHERE cm.conversation_id=$1 ORDER BY cm.actor_kind,u.name,v.name`
	if conversation.VisibleToSpace {
		query = `SELECT 'person',sm.user_id,'',u.name,u.email,NULL,sm.joined_at
			FROM space_members sm JOIN users u ON u.id=sm.user_id
			WHERE sm.space_id=$1 ORDER BY u.name,u.email`
	}
	argument := conversation.ID
	arguments := []any{argument, conversation.SpaceID}
	if conversation.VisibleToSpace {
		argument = conversation.SpaceID
		arguments = []any{argument}
	}
	rows, err := tx.QueryContext(ctx, query, arguments...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var participant SpaceConversationParticipant
		var avatar []byte
		if err := rows.Scan(&participant.Kind, &participant.UserID, &participant.AgentID, &participant.Name, &participant.Email, &avatar, &participant.JoinedAt); err != nil {
			return err
		}
		if len(avatar) > 0 {
			participant.Avatar = append(json.RawMessage(nil), avatar...)
		}
		conversation.Participants = append(conversation.Participants, participant)
	}
	return rows.Err()
}

func (db *Database) SpaceConversations(ctx context.Context, userID, spaceID string) ([]SpaceConversation, error) {
	items := []SpaceConversation{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionMessagesRead); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT c.id,c.space_id,c.title,c.kind,c.created_by_user_id,
			c.origin,COALESCE(c.integration_id,''),c.external_resource_id,c.external_display_name,
			c.integration_status,c.visible_to_space,COALESCE(c.direct_user_id,''),COALESCE(c.direct_agent_id,''),c.created_at,c.updated_at
			FROM space_conversations c
			LEFT JOIN space_conversation_members cm ON cm.conversation_id=c.id AND cm.user_id=$2
			WHERE c.space_id=$1 AND (c.visible_to_space OR cm.user_id=$2)
			ORDER BY c.updated_at DESC,c.id`, spaceID, userID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var item SpaceConversation
			if err := rows.Scan(&item.ID, &item.SpaceID, &item.Title, &item.Kind, &item.CreatedByUserID,
				&item.Origin, &item.IntegrationID, &item.ExternalResourceID, &item.ExternalDisplayName,
				&item.IntegrationStatus, &item.VisibleToSpace, &item.DirectUserID, &item.DirectAgentID, &item.CreatedAt, &item.UpdatedAt); err != nil {
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
			if err := loadSpaceConversationParticipantsTx(ctx, tx, &items[index]); err != nil {
				return err
			}
		}
		return nil
	})
	return items, err
}

// IsSpaceConversationForMember distinguishes a selected-member group
// conversation from the message correlation ID used by the Space-wide chat.
// If the ID belongs to a selected group, the caller must still be a member;
// otherwise returning false here could accidentally redirect a private reply
// into the Space-wide conversation.
func (db *Database) IsSpaceConversationForMember(ctx context.Context, userID, spaceID, conversationID string) (bool, error) {
	if strings.TrimSpace(conversationID) == "" {
		return false, nil
	}
	selectedGroup := false
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionMessagesWrite); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(
			SELECT 1 FROM space_conversations WHERE id=$1 AND space_id=$2
		)`, conversationID, spaceID).Scan(&selectedGroup); err != nil {
			return err
		}
		if !selectedGroup {
			return nil
		}
		return requireSpaceConversationMemberTx(ctx, tx, userID, spaceID, conversationID)
	})
	return selectedGroup, err
}

func (db *Database) CreateSpaceConversation(ctx context.Context, userID, spaceID, title string, participantRefs []SpaceActorRef) (*SpaceConversation, error) {
	title, err := normalizeConversationTitle(title)
	if err != nil {
		return nil, err
	}
	participants := normalizeSpaceActorRefs(append(participantRefs, SpaceActorRef{Kind: "person", UserID: userID}))
	if len(participants) < 2 {
		return nil, ErrSpaceInvalid
	}
	out := &SpaceConversation{
		ID: "space_conversation_" + uuid.NewString(), SpaceID: spaceID, Title: title,
		Kind: "standard", CreatedByUserID: userID, Origin: "misty", IntegrationStatus: "active",
	}
	err = db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpaceMessageWriteTx(ctx, tx, userID, spaceID); err != nil {
			return err
		}
		if misty, err := isMistySpaceTx(ctx, tx, spaceID); err != nil {
			return err
		} else if misty {
			if err := requireSpaceLifecycleManagerTx(ctx, tx, spaceID, userID); err != nil {
				return err
			}
		}
		if err := validateSpaceActorRefsTx(ctx, tx, userID, spaceID, participants); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `INSERT INTO space_conversations(id,space_id,title,created_by_user_id)
			VALUES($1,$2,$3,$4) RETURNING created_at,updated_at`, out.ID, spaceID, title, userID).
			Scan(&out.CreatedAt, &out.UpdatedAt); err != nil {
			return err
		}
		for _, participant := range participants {
			if _, err := tx.ExecContext(ctx, `INSERT INTO space_conversation_members(conversation_id,user_id,agent_id,actor_kind) VALUES($1,NULLIF($2,''),NULLIF($3,''),$4)`, out.ID, participant.UserID, participant.AgentID, participant.Kind); err != nil {
				return err
			}
		}
		if _, err := recordSpaceEventTx(ctx, tx, spaceID, userID, "conversation.created", out.ID, map[string]any{"conversation_id": out.ID, "title": title, "participants": participants}); err != nil {
			return err
		}
		return loadSpaceConversationParticipantsTx(ctx, tx, out)
	})
	return out, err
}

func normalizeSpaceActorRefs(values []SpaceActorRef) []SpaceActorRef {
	seen := map[string]bool{}
	out := make([]SpaceActorRef, 0, len(values))
	for _, value := range values {
		value.Kind, value.UserID, value.AgentID = strings.TrimSpace(value.Kind), strings.TrimSpace(value.UserID), strings.TrimSpace(value.AgentID)
		if value.Kind == "person" {
			value.AgentID = ""
		} else if value.Kind == "agent" {
			value.UserID = ""
		} else {
			continue
		}
		id := value.UserID
		if value.Kind == "agent" {
			id = value.AgentID
		}
		key := value.Kind + ":" + id
		if id != "" && !seen[key] {
			seen[key] = true
			out = append(out, value)
		}
	}
	return out
}

func validateSpaceActorRefsTx(ctx context.Context, tx *sql.Tx, userID, spaceID string, refs []SpaceActorRef) error {
	for _, ref := range refs {
		if ref.Kind == "person" {
			var exists bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM space_members WHERE space_id=$1 AND user_id=$2)`, spaceID, ref.UserID).Scan(&exists); err != nil || !exists {
				return ErrSpaceInvalid
			}
			continue
		}
		if _, err := activePersonalAgentMembershipTx(ctx, tx, userID, spaceID, ref.AgentID); err != nil {
			return ErrSpaceInvalid
		}
	}
	return nil
}

// DirectAgentConversation returns the one ordinary private Space conversation
// between the current person and an installed Agent, creating it atomically.
func (db *Database) DirectAgentConversation(ctx context.Context, userID, spaceID, agentID string) (*SpaceConversation, error) {
	out := &SpaceConversation{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpaceMessageWriteTx(ctx, tx, userID, spaceID); err != nil {
			return err
		}
		if err := requireStandardSpaceTx(ctx, tx, spaceID); err != nil {
			return err
		}
		membership, err := activePersonalAgentMembershipTx(ctx, tx, userID, spaceID, agentID)
		if err != nil {
			return err
		}
		id := "space_conversation_" + uuid.NewString()
		if err := tx.QueryRowContext(ctx, `INSERT INTO space_conversations(id,space_id,title,kind,created_by_user_id,direct_user_id,direct_agent_id)
			VALUES($1,$2,$3,'direct',$4,$4,$5)
			ON CONFLICT(space_id,direct_user_id,direct_agent_id) WHERE kind='direct' AND direct_agent_id IS NOT NULL
			DO UPDATE SET updated_at=space_conversations.updated_at
			RETURNING id,space_id,title,kind,created_by_user_id,created_at,updated_at`, id, spaceID, membership.Name, userID, agentID).
			Scan(&out.ID, &out.SpaceID, &out.Title, &out.Kind, &out.CreatedByUserID, &out.CreatedAt, &out.UpdatedAt); err != nil {
			return err
		}
		out.Origin, out.IntegrationStatus, out.DirectUserID, out.DirectAgentID = "misty", "active", userID, agentID
		if _, err := tx.ExecContext(ctx, `INSERT INTO space_conversation_members(conversation_id,user_id,actor_kind) VALUES($1,$2,'person') ON CONFLICT DO NOTHING`, out.ID, userID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO space_conversation_members(conversation_id,agent_id,actor_kind) VALUES($1,$2,'agent') ON CONFLICT DO NOTHING`, out.ID, agentID); err != nil {
			return err
		}
		return loadSpaceConversationParticipantsTx(ctx, tx, out)
	})
	return out, err
}
