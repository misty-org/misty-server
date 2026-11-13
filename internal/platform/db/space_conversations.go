package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

type SpaceActorRef struct {
	Kind   string `json:"kind"`
	UserID string `json:"user_id,omitempty"`
}

type SpaceConversationParticipant struct {
	SpaceActorRef
	Name     string    `json:"name"`
	Email    string    `json:"email,omitempty"`
	JoinedAt time.Time `json:"joined_at"`
}

type SpaceConversation struct {
	ID              string                         `json:"id"`
	SpaceID         string                         `json:"space_id"`
	Title           string                         `json:"title"`
	Kind            string                         `json:"kind"`
	CreatedByUserID string                         `json:"created_by_user_id"`
	VisibleToSpace  bool                           `json:"visible_to_space"`
	Participants    []SpaceConversationParticipant `json:"participants"`
	// UnreadCount is how many messages from others the caller hasn't read yet, capped at
	// spaceUnreadCap. Only the conversation list fills it.
	UnreadCount int64     `json:"unread_count"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func normalizeConversationTitle(title string) (string, error) {
	title = strings.TrimSpace(title)
	if title == "" || utf8.RuneCountInString(title) > 80 {
		return "", ErrSpaceInvalid
	}
	return title, nil
}

func privateMemberConversationTitle(left, right string) string {
	title := strings.TrimSpace(left) + " & " + strings.TrimSpace(right)
	runes := []rune(title)
	if len(runes) > 80 {
		title = string(runes[:79]) + "…"
	}
	return title
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
	query := `SELECT cm.user_id,u.username,u.email,cm.joined_at
		FROM space_conversation_members cm JOIN users u ON u.id=cm.user_id
		WHERE cm.conversation_id=$1 ORDER BY u.username`
	if conversation.VisibleToSpace {
		query = `SELECT sm.user_id,u.username,u.email,sm.joined_at
			FROM space_members sm JOIN users u ON u.id=sm.user_id
			WHERE sm.space_id=$1 ORDER BY u.username,u.email`
	}
	argument := conversation.ID
	arguments := []any{argument}
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
		participant := SpaceConversationParticipant{SpaceActorRef: SpaceActorRef{Kind: "person"}}
		if err := rows.Scan(&participant.UserID, &participant.Name, &participant.Email, &participant.JoinedAt); err != nil {
			return err
		}
		conversation.Participants = append(conversation.Participants, participant)
	}
	return rows.Err()
}

// spaceUnreadCap bounds an unread count: past it the app shows "99+", and counting stops there.
const spaceUnreadCap = 100

func (db *Database) SpaceConversations(ctx context.Context, userID, spaceID string) ([]SpaceConversation, error) {
	items, _, err := db.spaceConversations(ctx, userID, spaceID, false)
	return items, err
}

// SpaceConversationsWithUnread lists the caller's conversations with each one's unread count,
// and the unread count of the Space's Everyone chat. A message is unread when someone else sent
// it after the caller's read position and after they joined, so joining a busy Space doesn't
// arrive with everything already unread.
func (db *Database) SpaceConversationsWithUnread(ctx context.Context, userID, spaceID string) ([]SpaceConversation, int64, error) {
	return db.spaceConversations(ctx, userID, spaceID, true)
}

func (db *Database) spaceConversations(ctx context.Context, userID, spaceID string, withUnread bool) ([]SpaceConversation, int64, error) {
	items := []SpaceConversation{}
	var everyoneUnread int64
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionMessagesRead); err != nil {
			return err
		}
		unread, args := `0`, []any{spaceID, userID}
		if withUnread {
			args = append(args, spaceUnreadCap)
			unread = `(SELECT count(*) FROM (SELECT 1 FROM space_messages m
				WHERE m.space_id=c.space_id AND m.conversation_id=c.id AND m.sender_user_id<>$2
				  AND m.seq>COALESCE((SELECT r.read_message_seq FROM space_conversation_reads r
				      WHERE r.conversation_id=c.id AND r.user_id=$2),0)
				  AND (cm.joined_at IS NULL OR m.created_at>=cm.joined_at)
				LIMIT $3) unread)`
		}
		rows, err := tx.QueryContext(ctx, `SELECT c.id,c.space_id,c.title,c.kind,c.created_by_user_id,
			c.visible_to_space,c.created_at,c.updated_at,`+unread+`
			FROM space_conversations c
			LEFT JOIN space_conversation_members cm ON cm.conversation_id=c.id AND cm.user_id=$2
			WHERE c.space_id=$1 AND (c.visible_to_space OR cm.user_id=$2)
			ORDER BY c.updated_at DESC,c.id`, args...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var item SpaceConversation
			if err := rows.Scan(&item.ID, &item.SpaceID, &item.Title, &item.Kind, &item.CreatedByUserID,
				&item.VisibleToSpace, &item.CreatedAt, &item.UpdatedAt, &item.UnreadCount); err != nil {
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
		if !withUnread {
			return nil
		}
		return tx.QueryRowContext(ctx, `SELECT count(*) FROM (SELECT 1 FROM space_messages m
			JOIN space_members sm ON sm.space_id=m.space_id AND sm.user_id=$2
			WHERE m.space_id=$1 AND m.conversation_id IS NULL AND m.sender_user_id<>$2
			  AND m.seq>sm.read_message_seq AND m.created_at>=sm.joined_at
			LIMIT $3) unread`, spaceID, userID, spaceUnreadCap).Scan(&everyoneUnread)
	})
	return items, everyoneUnread, err
}

// CreateSpaceConversation makes a group chat, which is public to the Space:
// everyone in the Space is in it, and everyone who joins later joins it
// (space_members_join_group_chats). Naming people adds nothing, though each named
// person must still be in the Space. A private chat is a DM.
func (db *Database) CreateSpaceConversation(ctx context.Context, userID, spaceID, title string, participantRefs []SpaceActorRef) (*SpaceConversation, error) {
	title, err := normalizeConversationTitle(title)
	if err != nil {
		return nil, err
	}
	participants := normalizeSpaceActorRefs(participantRefs)
	out := &SpaceConversation{
		ID: "space_conversation_" + uuid.NewString(), SpaceID: spaceID, Title: title,
		Kind: "standard", CreatedByUserID: userID, VisibleToSpace: true,
	}
	err = db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpaceMessageWriteTx(ctx, tx, userID, spaceID); err != nil {
			return err
		}
		if err := validateSpaceActorRefsTx(ctx, tx, userID, spaceID, participants); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `INSERT INTO space_conversations(id,space_id,title,created_by_user_id,visible_to_space)
			VALUES($1,$2,$3,$4,TRUE) RETURNING created_at,updated_at`, out.ID, spaceID, title, userID).
			Scan(&out.CreatedAt, &out.UpdatedAt); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO space_conversation_members(conversation_id,user_id)
			SELECT $1,user_id FROM space_members WHERE space_id=$2`, out.ID, spaceID); err != nil {
			return err
		}
		if _, err := recordSpaceEventTx(ctx, tx, spaceID, userID, "conversation.created", out.ID, map[string]any{"conversation_id": out.ID, "title": title}); err != nil {
			return err
		}
		return loadSpaceConversationParticipantsTx(ctx, tx, out)
	})
	return out, err
}

// DirectMemberConversation returns the one DM between two people in a Space,
// creating it atomically. Only those two ever see it: not the rest of the Space,
// and not the Space's owner.
func (db *Database) DirectMemberConversation(ctx context.Context, userID, spaceID, targetUserID string) (*SpaceConversation, error) {
	userID, targetUserID = strings.TrimSpace(userID), strings.TrimSpace(targetUserID)
	if userID == "" || targetUserID == "" || userID == targetUserID {
		return nil, ErrSpaceInvalid
	}
	ids := []string{userID, targetUserID}
	if ids[1] < ids[0] {
		ids[0], ids[1] = ids[1], ids[0]
	}
	out := &SpaceConversation{}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpaceMessageWriteTx(ctx, tx, userID, spaceID); err != nil {
			return err
		}
		var leftName, rightName string
		if err := tx.QueryRowContext(ctx, `SELECT
			COALESCE((SELECT u.username FROM space_members sm JOIN users u ON u.id=sm.user_id WHERE sm.space_id=$1 AND sm.user_id=$2),''),
			COALESCE((SELECT u.username FROM space_members sm JOIN users u ON u.id=sm.user_id WHERE sm.space_id=$1 AND sm.user_id=$3),'')`, spaceID, ids[0], ids[1]).Scan(&leftName, &rightName); err != nil {
			return err
		}
		if leftName == "" || rightName == "" {
			return ErrSpaceInvalid
		}
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "spaces:member-conversation:"+spaceID+":"+ids[0]+":"+ids[1]); err != nil {
			return err
		}
		err := tx.QueryRowContext(ctx, `SELECT c.id,c.space_id,c.title,c.kind,c.created_by_user_id,c.created_at,c.updated_at
			FROM space_conversations c
			WHERE c.space_id=$1 AND c.kind='dm'
			  AND (SELECT count(*) FROM space_conversation_members cm WHERE cm.conversation_id=c.id)=2
			  AND EXISTS(SELECT 1 FROM space_conversation_members cm WHERE cm.conversation_id=c.id AND cm.user_id=$2)
			  AND EXISTS(SELECT 1 FROM space_conversation_members cm WHERE cm.conversation_id=c.id AND cm.user_id=$3)
			ORDER BY c.created_at LIMIT 1`, spaceID, ids[0], ids[1]).Scan(&out.ID, &out.SpaceID, &out.Title, &out.Kind, &out.CreatedByUserID, &out.CreatedAt, &out.UpdatedAt)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if errors.Is(err, sql.ErrNoRows) {
			out.ID = "space_conversation_" + uuid.NewString()
			out.SpaceID, out.Title, out.Kind, out.CreatedByUserID = spaceID, privateMemberConversationTitle(leftName, rightName), "dm", userID
			if err := tx.QueryRowContext(ctx, `INSERT INTO space_conversations(id,space_id,title,created_by_user_id,kind)
				VALUES($1,$2,$3,$4,'dm') RETURNING created_at,updated_at`, out.ID, spaceID, out.Title, userID).Scan(&out.CreatedAt, &out.UpdatedAt); err != nil {
				return err
			}
			for _, memberID := range ids {
				if _, err := tx.ExecContext(ctx, `INSERT INTO space_conversation_members(conversation_id,user_id) VALUES($1,$2)`, out.ID, memberID); err != nil {
					return err
				}
			}
			if _, err := recordSpaceEventTx(ctx, tx, spaceID, userID, "conversation.created", out.ID, map[string]any{"conversation_id": out.ID, "title": out.Title, "participants": []SpaceActorRef{{Kind: "person", UserID: ids[0]}, {Kind: "person", UserID: ids[1]}}}); err != nil {
				return err
			}
		}
		out.VisibleToSpace = false
		return loadSpaceConversationParticipantsTx(ctx, tx, out)
	})
	return out, err
}

func normalizeSpaceActorRefs(values []SpaceActorRef) []SpaceActorRef {
	seen := map[string]bool{}
	out := make([]SpaceActorRef, 0, len(values))
	for _, value := range values {
		value.Kind, value.UserID = strings.TrimSpace(value.Kind), strings.TrimSpace(value.UserID)
		if value.Kind == "person" && value.UserID != "" && !seen[value.UserID] {
			seen[value.UserID] = true
			out = append(out, value)
		}
	}
	return out
}

func validateSpaceActorRefsTx(ctx context.Context, tx *sql.Tx, userID, spaceID string, refs []SpaceActorRef) error {
	for _, ref := range refs {
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM space_members WHERE space_id=$1 AND user_id=$2)`, spaceID, ref.UserID).Scan(&exists); err != nil || !exists {
			return ErrSpaceInvalid
		}
	}
	return nil
}
