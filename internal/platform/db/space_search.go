package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"unicode"
)

// SpaceSearchMaxQuery is the longest search text a route accepts.
const SpaceSearchMaxQuery = 500

const spaceSearchMaxWords = 32

// spaceNoteSearchVector is the expression space_notes_search_idx indexes, spelled
// the same so Postgres uses that index. Titles outrank tags, and tags outrank text.
const spaceNoteSearchVector = `(setweight(to_tsvector('simple'::regconfig,COALESCE(n.title_projection,'')),'A')
	|| setweight(to_tsvector('simple'::regconfig,COALESCE(n.shared_tags::text,'')),'B')
	|| setweight(to_tsvector('simple'::regconfig,COALESCE(n.plain_text_projection,'')),'C'))`

// spaceSearchQuery turns what someone typed into a tsquery: every word has to
// appear, as the start of a word, so "budg" finds "budget". Only letters, digits
// and marks reach it, so nothing in the text is tsquery syntax. Empty means the
// text has no words to look for.
func spaceSearchQuery(text string) string {
	words := strings.FieldsFunc(text, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && !unicode.IsMark(r)
	})
	terms := make([]string, 0, min(len(words), spaceSearchMaxWords))
	for _, word := range words[:min(len(words), spaceSearchMaxWords)] {
		terms = append(terms, "'"+word+"':*")
	}
	return strings.Join(terms, " & ")
}

// SearchSpaceNotes finds the notes a caller may view in one Space, best match
// first. It applies the same access rules as AccessibleSpaceNotes.
func (db *Database) SearchSpaceNotes(ctx context.Context, userID, spaceID, text string, limit int) ([]SpaceNote, error) {
	notes := []SpaceNote{}
	query := spaceSearchQuery(text)
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		if query == "" {
			return nil
		}
		rows, err := tx.QueryContext(ctx,
			`SELECT n.id,n.space_id,n.creator_user_id,n.title_projection,n.markdown_projection,n.plain_text_projection,n.lifecycle_state,
			        n.collaboration_revision,n.acl_version,n.audience_kind,COALESCE(n.audience_conversation_id,''),n.visibility,n.created_at,n.updated_at,
			        CASE WHEN n.creator_user_id=$1 THEN 'creator' ELSE 'editor' END AS effective_role,
			        (n.creator_user_id=$1 OR EXISTS(SELECT 1 FROM spaces s WHERE s.id=n.space_id AND s.owner_user_id=$1)) AS can_delete,
			        (SELECT COUNT(*) FROM space_note_links links
			         JOIN space_notes source ON source.id=links.source_note_id
			         WHERE links.target_note_id=n.id AND source.lifecycle_state='active'
			           AND (source.visibility='public' OR source.creator_user_id=$3)
			           AND (source.audience_kind='space' OR EXISTS(
			               SELECT 1 FROM space_conversation_members cm
			               WHERE cm.conversation_id=source.audience_conversation_id
			                 AND cm.user_id=$1))) AS backlink_count
			 FROM space_notes n CROSS JOIN to_tsquery('simple',$4) q
			 WHERE n.space_id=$2 AND n.lifecycle_state='active' AND `+spaceNoteSearchVector+` @@ q
			   AND (n.visibility='public' OR n.creator_user_id=$3)
			   AND (n.audience_kind='space' OR EXISTS(SELECT 1 FROM space_conversation_members cm WHERE cm.conversation_id=n.audience_conversation_id AND cm.user_id=$1))
			 ORDER BY ts_rank_cd(`+spaceNoteSearchVector+`,q) DESC,n.updated_at DESC,n.id LIMIT $5`,
			userID, spaceID, userID, query, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var note SpaceNote
			if err := rows.Scan(&note.ID, &note.SpaceID, &note.CreatorUserID, &note.TitleProjection,
				&note.MarkdownProjection, &note.PlainTextProjection, &note.LifecycleState, &note.CollaborationRevision, &note.ACLVersion, &note.AudienceKind, &note.AudienceConversationID,
				&note.Visibility, &note.CreatedAt, &note.UpdatedAt, &note.Role, &note.CanDelete, &note.BacklinkCount); err != nil {
				return err
			}
			notes = append(notes, note)
		}
		return rows.Err()
	})
	return notes, err
}

// spaceMessageVisibleTo is the one rule for who sees a message: the main chat
// is the whole Space's, and a conversation's messages belong to those who can
// open that conversation, as requireSpaceConversationMemberTx decides. The
// placeholder is the caller.
func spaceMessageVisibleTo(user string) string {
	return `(m.conversation_id IS NULL OR EXISTS(SELECT 1 FROM space_conversations visible
		WHERE visible.id=m.conversation_id AND (visible.visible_to_space OR EXISTS(
			SELECT 1 FROM space_conversation_members cm WHERE cm.conversation_id=visible.id AND cm.user_id=` + user + `))))`
}

// SearchSpaceMessages finds messages the caller can see in one Space, in its
// main chat and in the conversations they can open, best match first.
func (db *Database) SearchSpaceMessages(ctx context.Context, userID, spaceID, text string, limit int) ([]SpaceMessage, error) {
	items := []SpaceMessage{}
	query := spaceSearchQuery(text)
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionMessagesRead); err != nil {
			return err
		}
		if query == "" {
			return nil
		}
		rows, err := tx.QueryContext(ctx, `SELECT `+spaceMessageColumns+` FROM space_messages m
			LEFT JOIN users u ON u.id=m.sender_user_id
			CROSS JOIN to_tsquery('simple',$2) q
			WHERE m.space_id=$1 AND m.search_tsv @@ q AND `+spaceMessageVisibleTo("$3")+`
			ORDER BY ts_rank_cd(m.search_tsv,q) DESC,m.seq DESC LIMIT $4`, spaceID, query, userID, limit)
		if err != nil {
			return err
		}
		for rows.Next() {
			var item SpaceMessage
			if err := scanSpaceMessage(rows, &item); err != nil {
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
			if err := loadSpaceMessageReferencesTx(ctx, tx, &items[index], userID); err != nil {
				return err
			}
		}
		return nil
	})
	return items, err
}

// SpaceSearchHit is one note or message that matched a search across Spaces.
type SpaceSearchHit struct {
	Kind      string
	ID        string
	Title     string
	Text      string
	SpaceID   string
	SpaceName string
}

// SearchSpaces searches notes and messages together, ranked against each other,
// across every Space the caller belongs to or just spaceID. Every member may
// read a Space's messages, so membership is the check that decides which
// Spaces are searched (hasSpacePermissionTx).
func (db *Database) SearchSpaces(ctx context.Context, userID, text, spaceID string, notes, messages bool, limit int) ([]SpaceSearchHit, error) {
	hits := []SpaceSearchHit{}
	query := spaceSearchQuery(text)
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if spaceID != "" {
			if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
				return err
			}
		}
		if query == "" {
			return nil
		}
		rows, err := tx.QueryContext(ctx, `
			WITH mine AS (
				SELECT s.id,s.name FROM spaces s JOIN space_members member ON member.space_id=s.id
				WHERE member.user_id=$1 AND s.lifecycle_state='active' AND ($3='' OR s.id=$3)
			)
			SELECT kind,id,title,body,space_id,space_name FROM (
				SELECT 'note' kind,n.id,n.title_projection title,n.plain_text_projection body,n.space_id,mine.name space_name,
					ts_rank_cd(`+spaceNoteSearchVector+`,q) score,n.updated_at moment
				FROM space_notes n JOIN mine ON mine.id=n.space_id CROSS JOIN to_tsquery('simple',$2) q
				WHERE $5 AND n.lifecycle_state='active' AND `+spaceNoteSearchVector+` @@ q
				  AND (n.visibility='public' OR n.creator_user_id=$4)
				  AND (n.audience_kind='space' OR EXISTS(SELECT 1 FROM space_conversation_members cm WHERE cm.conversation_id=n.audience_conversation_id AND cm.user_id=$1))
				UNION ALL
				SELECT 'message',m.id,CASE WHEN m.conversation_id IS NULL THEN 'Space chat' ELSE COALESCE(NULLIF(c.title,''),'Conversation') END,m.content::text,m.space_id,mine.name,
					ts_rank_cd(m.search_tsv,q),m.created_at
				FROM space_messages m JOIN mine ON mine.id=m.space_id LEFT JOIN space_conversations c ON c.id=m.conversation_id
				CROSS JOIN to_tsquery('simple',$2) q
				WHERE $6 AND m.search_tsv @@ q AND `+spaceMessageVisibleTo("$1")+`
			) found ORDER BY score DESC,moment DESC,id LIMIT $7`,
			userID, query, spaceID, userID, notes, messages, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var hit SpaceSearchHit
			if err := rows.Scan(&hit.Kind, &hit.ID, &hit.Title, &hit.Text, &hit.SpaceID, &hit.SpaceName); err != nil {
				return err
			}
			if hit.Kind == "message" {
				hit.Text = spaceSearchMessageText(hit.Text)
			}
			hits = append(hits, hit)
		}
		return rows.Err()
	})
	return hits, err
}

func spaceSearchMessageText(raw string) string {
	var spans []MessageSpan
	if json.Unmarshal([]byte(raw), &spans) != nil {
		return ""
	}
	var text strings.Builder
	for _, span := range spans {
		switch {
		case span.Text != "":
			text.WriteString(span.Text)
		case span.Label != "":
			text.WriteString(span.Label)
		}
	}
	return strings.TrimSpace(text.String())
}
