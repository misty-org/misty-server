package db

import (
	"context"
	"database/sql"
	"strings"
)

const (
	SpaceAudienceSpace        = "space"
	SpaceAudienceConversation = "conversation"
	ConversationScopeEveryone = "everyone"
	ConversationScopePrivate  = "conversation"
)

// SpaceResourceAudience travels with every conversation-derived resource.
// CreatorUserID is intentionally omitted from ordinary audience serialization;
// resource records already expose their creator where appropriate.
type SpaceResourceAudience struct {
	Kind           string `json:"kind"`
	ConversationID string `json:"conversation_id,omitempty"`
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
