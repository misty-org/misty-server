package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

const (
	SpaceAudienceSpace        = "space"
	SpaceAudienceConversation = "conversation"
)

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
		SELECT 1 FROM space_conversations c WHERE c.id=$1 AND c.space_id=$2
	) AND `+conversationReadableSQL("$1", "$3"), audience.ConversationID, spaceID, userID).Scan(&member)
	if err != nil {
		return err
	}
	if !member {
		return ErrSpaceForbidden
	}
	return nil
}

var audienceTables = withSpaceItemAudiences(map[string]string{})

func withSpaceItemAudiences(others map[string]string) map[string]string {
	for _, kind := range spaceItemKinds {
		others[kind.Name] = kind.Table
	}
	return others
}

func itemAudienceReadableTx(ctx context.Context, tx *sql.Tx, userID, spaceID, kind, itemID string) (bool, error) {
	table, ok := audienceTables[kind]
	if !ok {
		return true, nil
	}
	var audienceKind, conversationID string
	err := tx.QueryRowContext(ctx, `SELECT audience_kind,COALESCE(audience_conversation_id,'') FROM `+table+` WHERE id=$1 AND space_id=$2`,
		itemID, spaceID).Scan(&audienceKind, &conversationID)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if audienceKind != SpaceAudienceConversation {
		return true, nil
	}
	return conversationReadableTx(ctx, tx, userID, spaceID, conversationID)
}

func resourceAudienceSQL(alias, viewerPlaceholder string) string {
	return "(" + alias + ".audience_kind='space' OR " + conversationReadableSQL(alias+".audience_conversation_id", viewerPlaceholder) + ")"
}
