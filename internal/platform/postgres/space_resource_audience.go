package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

const (
	SpaceAudienceSpace        = "space"
	SpaceAudienceConversation = "conversation"
	ConversationScopeEveryone = "everyone"
	ConversationScopePrivate  = "conversation"
)

// SpaceConversationScopeRef is the non-overloaded location of a conversation
// action. Everyone has no conversation ID; private/group/direct scopes must.
type SpaceConversationScopeRef struct {
	Kind           string `json:"kind"`
	ConversationID string `json:"conversation_id,omitempty"`
}

// SpaceResourceAudience travels with every conversation-derived resource.
// CreatorUserID is intentionally omitted from ordinary audience serialization;
// resource records already expose their creator where appropriate.
type SpaceResourceAudience struct {
	Kind           string `json:"kind"`
	ConversationID string `json:"conversation_id,omitempty"`
}

func NormalizeConversationScope(conversationID string) SpaceConversationScopeRef {
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return SpaceConversationScopeRef{Kind: ConversationScopeEveryone}
	}
	return SpaceConversationScopeRef{Kind: ConversationScopePrivate, ConversationID: conversationID}
}

func NormalizeResourceAudience(kind, conversationID string) (SpaceResourceAudience, error) {
	kind, conversationID = strings.TrimSpace(kind), strings.TrimSpace(conversationID)
	if kind == "" || kind == SpaceAudienceSpace {
		if conversationID != "" {
			return SpaceResourceAudience{}, ErrSpaceInvalid
		}
		return SpaceResourceAudience{Kind: SpaceAudienceSpace}, nil
	}
	if kind != SpaceAudienceConversation || conversationID == "" {
		return SpaceResourceAudience{}, ErrSpaceInvalid
	}
	return SpaceResourceAudience{Kind: kind, ConversationID: conversationID}, nil
}

func validateResourceAudienceTx(ctx context.Context, tx *sql.Tx, userID, spaceID string, audience SpaceResourceAudience) error {
	if audience.Kind == SpaceAudienceSpace {
		return nil
	}
	var member bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(
		SELECT 1 FROM space_conversation_members cm
		JOIN space_conversations c ON c.id=cm.conversation_id
		WHERE c.id=$1 AND c.space_id=$2 AND cm.actor_kind='person' AND cm.user_id=$3
	)`, audience.ConversationID, spaceID, userID).Scan(&member)
	if err != nil {
		return err
	}
	if !member {
		return ErrSpaceForbidden
	}
	return nil
}

func resourceAudienceForConversation(conversationID string) SpaceResourceAudience {
	if strings.TrimSpace(conversationID) == "" {
		return SpaceResourceAudience{Kind: SpaceAudienceSpace}
	}
	return SpaceResourceAudience{Kind: SpaceAudienceConversation, ConversationID: strings.TrimSpace(conversationID)}
}

// requireLibraryItemAudienceTx closes the gap created by service-role database
// connections: permission to use Library does not imply access to an item that
// belongs to a private conversation.
func requireLibraryItemAudienceTx(ctx context.Context, tx *sql.Tx, userID, spaceID, itemID string) error {
	var allowed bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(
		SELECT 1 FROM space_library_items item
		WHERE item.id=$1 AND item.space_id=$2 AND (
			item.audience_kind='space' OR EXISTS(
				SELECT 1 FROM space_conversation_members member
				WHERE member.conversation_id=item.audience_conversation_id
				  AND member.actor_kind='person' AND member.user_id=$3
			)
		)
	)`, itemID, spaceID, userID).Scan(&allowed)
	if err != nil {
		return err
	}
	if !allowed {
		return ErrLibraryNotFound
	}
	return nil
}

func resourceAudienceSQL(alias, viewerPlaceholder string) string {
	return "(" + alias + ".audience_kind='space' OR EXISTS(SELECT 1 FROM space_conversation_members audience_member WHERE audience_member.conversation_id=" + alias + ".audience_conversation_id AND audience_member.actor_kind='person' AND audience_member.user_id=" + viewerPlaceholder + "))"
}

// ShareSpaceResourceWithSpace is the sole audience-widening primitive. It is
// exposed only through human-authenticated endpoints and checks the immutable
// human creator column for the specific resource type.
func (db *Database) ShareSpaceResourceWithSpace(ctx context.Context, userID, spaceID, kind, resourceID string) error {
	table, creatorColumn := "", ""
	switch kind {
	case "task":
		table, creatorColumn = "space_tasks", "audience_creator_user_id"
	case "calendar_event":
		table, creatorColumn = "space_native_calendar_events", "created_by_user_id"
	case "note":
		table, creatorColumn = "space_notes", "creator_user_id"
	case "drawing":
		table, creatorColumn = "space_drawings", "creator_user_id"
	case "roadmap":
		table, creatorColumn = "space_roadmaps", "created_by_user_id"
	case "library_item":
		table, creatorColumn = "space_library_items", "added_by_user_id"
	default:
		return ErrSpaceInvalid
	}
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		query := fmt.Sprintf(`UPDATE %s SET audience_kind='space',audience_conversation_id=NULL,updated_at=NOW() WHERE id=$1 AND space_id=$2 AND audience_kind='conversation' AND %s=$3`, table, creatorColumn)
		result, err := tx.ExecContext(ctx, query, resourceID, spaceID, userID)
		if err != nil {
			return err
		}
		if n, _ := result.RowsAffected(); n != 1 {
			return ErrSpaceForbidden
		}
		_, err = recordSpaceEventTx(ctx, tx, spaceID, userID, kind+".shared_with_space", resourceID, map[string]any{"audience_kind": SpaceAudienceSpace})
		return err
	})
}
