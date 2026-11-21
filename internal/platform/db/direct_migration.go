package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
)

const directMigrationName = "space_dms_to_direct"

// MigrateSpaceDirectMessages moves every DM that still lives inside a Space (a conversation of
// kind 'dm') into a DM of its own. Each one moved is gone from its Space, so running it again
// moves nothing twice; direct_migrations records when it first finished. A one-to-one DM between the same two people in several
// Spaces becomes one DM, its messages in the order they were sent; a group DM becomes its own.
// Text, replies, reactions, threads and how far each person read come along. A file sent in one
// stays in its Space's storage, so the message says what the file was and where it stayed.
// Two people who aren't friends keep their history read-only until they are.
func (db *Database) MigrateSpaceDirectMessages(ctx context.Context) error {
	ids := []string{}
	if err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT c.id FROM space_conversations c JOIN spaces s ON s.id=c.space_id
			WHERE c.kind='dm' ORDER BY c.created_at,c.id`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return err
			}
			ids = append(ids, id)
		}
		return rows.Err()
	}); err != nil {
		return err
	}
	for _, id := range ids {
		if err := db.SpaceTx(ctx, func(tx *sql.Tx) error { return moveSpaceDirectTx(ctx, tx, id) }); err != nil {
			return err
		}
	}
	if len(ids) > 0 {
		log.Printf("moved %d DMs out of Spaces", len(ids))
	}
	return db.SpaceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO direct_migrations(name) VALUES($1) ON CONFLICT DO NOTHING`, directMigrationName)
		return err
	})
}

func moveSpaceDirectTx(ctx context.Context, tx *sql.Tx, conversationID string) error {
	var spaceID, spaceName, createdBy string
	err := tx.QueryRowContext(ctx, `SELECT c.space_id,s.name,COALESCE(c.created_by_user_id,'') FROM space_conversations c JOIN spaces s ON s.id=c.space_id
		WHERE c.id=$1 AND c.kind='dm' FOR UPDATE OF c`, conversationID).Scan(&spaceID, &spaceName, &createdBy)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	people := []string{}
	rows, err := tx.QueryContext(ctx, `SELECT cm.user_id FROM space_conversation_members cm JOIN users u ON u.id=cm.user_id AND u.lifecycle_state='active'
		WHERE cm.conversation_id=$1 ORDER BY cm.joined_at,cm.user_id`, conversationID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		people = append(people, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if len(people) < 2 {
		// Nobody left to talk to: the DM goes with its Space conversation.
		return dropSpaceDirectTx(ctx, tx, conversationID)
	}
	starter := people[0]
	for _, id := range people {
		if id == createdBy {
			starter = id
		}
	}
	others := []string{}
	for _, id := range people {
		if id != starter {
			others = append(others, id)
		}
	}
	var target string
	if len(people) == 2 {
		key := directPairKey(people[0], people[1])
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "dms:pair:"+key); err != nil {
			return err
		}
		err := tx.QueryRowContext(ctx, `SELECT id FROM spaces WHERE kind='direct' AND direct_key=$1 AND lifecycle_state='active'`, key).Scan(&target)
		if errors.Is(err, sql.ErrNoRows) {
			target, err = createDirectSpaceTx(ctx, tx, starter, others, key)
		}
		if err != nil {
			return err
		}
	} else {
		if len(people) > maxDirectPeople {
			people, others = people[:maxDirectPeople], others[:maxDirectPeople-1]
		}
		target, err = createDirectSpaceTx(ctx, tx, starter, others, "")
		if err != nil {
			return err
		}
	}
	// A file stays in the Space it was sent in; its message says so.
	attachments, err := tx.QueryContext(ctx, `SELECT a.message_id,a.display_name FROM space_message_attachments a JOIN space_messages m ON m.id=a.message_id
		WHERE m.conversation_id=$1 ORDER BY a.created_at,a.id`, conversationID)
	if err != nil {
		return err
	}
	notes := map[string][]string{}
	for attachments.Next() {
		var messageID, name string
		if err := attachments.Scan(&messageID, &name); err != nil {
			attachments.Close()
			return err
		}
		notes[messageID] = append(notes[messageID], name)
	}
	if err := attachments.Err(); err != nil {
		attachments.Close()
		return err
	}
	attachments.Close()
	for messageID, names := range notes {
		var raw []byte
		if err := tx.QueryRowContext(ctx, `SELECT content FROM space_messages WHERE id=$1`, messageID).Scan(&raw); err != nil {
			return err
		}
		content := []MessageSpan{}
		if err := json.Unmarshal(raw, &content); err != nil {
			return err
		}
		for _, name := range names {
			if len(content) > 0 {
				content = append(content, MessageSpan{Type: "text", Text: "\n"})
			}
			content = append(content, MessageSpan{Type: "text", Text: "📎 " + name + " (kept in " + spaceName + ")"})
		}
		updated, err := json.Marshal(content)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE space_messages SET content=$2,file_node_ids='{}' WHERE id=$1`, messageID, updated); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE space_message_attachments SET message_id=NULL WHERE message_id IN (SELECT id FROM space_messages WHERE conversation_id=$1)`, conversationID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE space_message_reactions SET space_id=$2 WHERE message_id IN (SELECT id FROM space_messages WHERE conversation_id=$1)`, conversationID, target); err != nil {
		return err
	}
	// Mentions of the old Space's members stay as written; the DM's people are the same people.
	if _, err := tx.ExecContext(ctx, `UPDATE space_messages SET space_id=$2,conversation_id=NULL,expires_at=NULL WHERE conversation_id=$1`, conversationID, target); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE space_members m SET read_message_seq=GREATEST(m.read_message_seq,r.read_message_seq)
		FROM space_conversation_reads r WHERE r.conversation_id=$1 AND r.user_id=m.user_id AND m.space_id=$2`, conversationID, target); err != nil {
		return err
	}
	return dropSpaceDirectTx(ctx, tx, conversationID)
}

// dropSpaceDirectTx removes the emptied Space conversation; its messages have moved, and its
// members, reads and listed agents go with it.
func dropSpaceDirectTx(ctx context.Context, tx *sql.Tx, conversationID string) error {
	if _, err := tx.ExecContext(ctx, `UPDATE space_message_attachments SET message_id=NULL WHERE message_id IN (SELECT id FROM space_messages WHERE conversation_id=$1)`, conversationID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM space_messages WHERE conversation_id=$1`, conversationID); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM space_conversations WHERE id=$1`, conversationID)
	return err
}
