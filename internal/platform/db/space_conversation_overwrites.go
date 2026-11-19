package db

import (
	"context"
	"database/sql"
	"errors"

	"github.com/lib/pq"
	"github.com/misty-org/misty-server/internal/spaces"
)

// SpaceConversationOverwrite changes channel-scoped permissions for one role
// in one standard channel.
type SpaceConversationOverwrite struct {
	RoleID string   `json:"role_id"`
	Allow  []string `json:"allow"`
	Deny   []string `json:"deny"`
}

// spaceConversationEvent is a whole conversation as a conversation.updated
// event carries it. Permissions are each reader's own, so it carries none.
type spaceConversationEvent struct {
	ConversationID string `json:"conversation_id"`
	SpaceConversation
	Permissions []string `json:"permissions,omitempty"`
}

func domainOverwrites(values []SpaceConversationOverwrite) []spaces.Overwrite {
	out := make([]spaces.Overwrite, 0, len(values))
	for _, value := range values {
		out = append(out, spaces.Overwrite{RoleID: value.RoleID, Allow: value.Allow, Deny: value.Deny})
	}
	return out
}

// conversationOverwritesTx lists a conversation's overwrites, everyone's first.
func conversationOverwritesTx(ctx context.Context, tx *sql.Tx, conversationID string) ([]SpaceConversationOverwrite, error) {
	rows, err := tx.QueryContext(ctx, `SELECT o.role_id,o.allow,o.deny FROM space_conversation_overwrites o
		JOIN space_roles r ON r.id=o.role_id WHERE o.conversation_id=$1 ORDER BY r.position,o.role_id`, conversationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SpaceConversationOverwrite{}
	for rows.Next() {
		item := SpaceConversationOverwrite{Allow: []string{}, Deny: []string{}}
		if err := rows.Scan(&item.RoleID, pq.Array(&item.Allow), pq.Array(&item.Deny)); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func insertConversationOverwritesTx(ctx context.Context, tx *sql.Tx, conversationID string, overwrites []spaces.Overwrite) error {
	for _, overwrite := range overwrites {
		if _, err := tx.ExecContext(ctx, `INSERT INTO space_conversation_overwrites(conversation_id,role_id,allow,deny)
			VALUES($1,$2,$3,$4)`, conversationID, overwrite.RoleID, pq.Array(overwrite.Allow), pq.Array(overwrite.Deny)); err != nil {
			return err
		}
	}
	return nil
}

// requireSpaceRoleIDsTx checks that every overwrite names a role of this Space.
func requireSpaceRoleIDsTx(ctx context.Context, tx *sql.Tx, spaceID string, overwrites []spaces.Overwrite) error {
	for _, overwrite := range overwrites {
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM space_roles WHERE space_id=$1 AND id::text=$2)`,
			spaceID, overwrite.RoleID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return ErrSpaceInvalid
		}
	}
	return nil
}

// fillConversationAccessTx sets a conversation's overwrites and the caller's
// permissions in it.
func fillConversationAccessTx(ctx context.Context, tx *sql.Tx, access *memberAccess, conversation *SpaceConversation) error {
	conversation.Overwrites = []SpaceConversationOverwrite{}
	if conversation.Kind != "standard" {
		conversation.Permissions = spaces.DirectPermissions(access.base).List()
		return nil
	}
	overwrites, err := conversationOverwritesTx(ctx, tx, conversation.ID)
	if err != nil {
		return err
	}
	conversation.Overwrites = overwrites
	conversation.Permissions = spaces.ChannelPermissions(access.base, access.everyoneRoleID, access.roleIDs, domainOverwrites(overwrites)).List()
	return nil
}

// loadSpaceConversationTx loads one conversation with its participants and
// overwrites, but nobody's permissions.
func loadSpaceConversationTx(ctx context.Context, tx *sql.Tx, spaceID, conversationID string) (*SpaceConversation, error) {
	out := &SpaceConversation{}
	err := tx.QueryRowContext(ctx, `SELECT id,space_id,title,kind,created_by_user_id,visible_to_space,created_at,updated_at
		FROM space_conversations WHERE id=$1 AND space_id=$2`, conversationID, spaceID).
		Scan(&out.ID, &out.SpaceID, &out.Title, &out.Kind, &out.CreatedByUserID, &out.VisibleToSpace, &out.CreatedAt, &out.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := loadSpaceConversationParticipantsTx(ctx, tx, out); err != nil {
		return nil, err
	}
	out.Overwrites = []SpaceConversationOverwrite{}
	if out.Kind == "standard" {
		if out.Overwrites, err = conversationOverwritesTx(ctx, tx, conversationID); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// recordConversationUpdatedTx announces a whole conversation as
// conversation.updated. Each reader gets it only while they can read the
// channel (spaceEventVisibleToUserTx), so it also shows a channel to someone
// who just gained it.
func recordConversationUpdatedTx(ctx context.Context, tx *sql.Tx, spaceID, actorID, conversationID string) error {
	conversation, err := loadSpaceConversationTx(ctx, tx, spaceID, conversationID)
	if err != nil {
		return err
	}
	_, err = recordSpaceEventTx(ctx, tx, spaceID, actorID, "conversation.updated", conversationID,
		spaceConversationEvent{ConversationID: conversationID, SpaceConversation: *conversation})
	return err
}

// SetSpaceConversationOverwrites replaces a standard channel's overwrites. It
// takes channels.manage in that channel, and every permission it changes must
// be one the caller holds there. Whoever can no longer read the channel gets
// conversation.removed; everyone who can gets conversation.updated.
func (db *Database) SetSpaceConversationOverwrites(ctx context.Context, userID, spaceID, conversationID string, overwrites []SpaceConversationOverwrite) (*SpaceConversation, error) {
	normalized, ok := spaces.NormalizeOverwrites(domainOverwrites(overwrites))
	if !ok {
		return nil, ErrSpaceInvalid
	}
	var out *SpaceConversation
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if err := lockSpaceRolesTx(ctx, tx, spaceID); err != nil {
			return err
		}
		access, err := memberAccessTx(ctx, tx, spaceID, userID)
		if err != nil {
			return err
		}
		permissions, kind, err := channelPermissionsForTx(ctx, tx, access, userID, spaceID, conversationID)
		if err != nil {
			return err
		}
		if !permissions.Has(PermissionMessagesRead) {
			return ErrSpaceNotFound
		}
		if kind != "standard" || !permissions.Has(PermissionChannelsManage) {
			return ErrSpaceForbidden
		}
		if err := requireSpaceRoleIDsTx(ctx, tx, spaceID, normalized); err != nil {
			return err
		}
		before, err := conversationOverwritesTx(ctx, tx, conversationID)
		if err != nil {
			return err
		}
		if !access.owner && !spaces.OverwriteChangesHeld(permissions, domainOverwrites(before), normalized) {
			return ErrSpaceForbidden
		}
		readers, err := channelReadersTx(ctx, tx, spaceID)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM space_conversation_overwrites WHERE conversation_id=$1`, conversationID); err != nil {
			return err
		}
		if err := insertConversationOverwritesTx(ctx, tx, conversationID, normalized); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE space_conversations SET updated_at=NOW() WHERE id=$1`, conversationID); err != nil {
			return err
		}
		if err := announceChannelAccessTx(ctx, tx, spaceID, userID, readers, conversationID); err != nil {
			return err
		}
		if err := recordConversationUpdatedTx(ctx, tx, spaceID, userID, conversationID); err != nil {
			return err
		}
		if out, err = loadSpaceConversationTx(ctx, tx, spaceID, conversationID); err != nil {
			return err
		}
		return fillConversationAccessTx(ctx, tx, access, out)
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
