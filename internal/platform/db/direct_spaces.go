package db

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
)

// maxDirectPeople is how many people one group DM holds, its starter included.
const maxDirectPeople = 10

// directSpaceName is what a DM Space is called underneath; people see the others' names instead.
const directSpaceName = "Direct message"

// DirectConversation is a DM: a Space of kind 'direct' whose "everyone" chat is the conversation.
// Its id is the Space's id, so the Space chat routes read and write its messages.
type DirectConversation struct {
	ID string `json:"id"`
	// Kind is "direct" for two people, "group" for three to ten.
	Kind    string   `json:"kind"`
	Members []Person `json:"members"`
	// UnreadCount is messages from others the caller hasn't read, up to 100.
	UnreadCount   int        `json:"unread_count"`
	LastMessageAt *time.Time `json:"last_message_at,omitempty"`
	// ReadOnly is a one-to-one DM whose two people are no longer friends (one moved from a Space):
	// its history stays, and it opens again once they are friends.
	ReadOnly  bool      `json:"read_only"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func directPairKey(a, b string) string {
	low, high := friendPair(a, b)
	return low + ":" + high
}

// requireFriendsTx refuses unless the two are friends and neither blocked the other.
func requireFriendsTx(ctx context.Context, tx *sql.Tx, userID, otherID string) error {
	if userID == otherID {
		return ErrSpaceInvalid
	}
	if blocked, err := blockedEitherWayTx(ctx, tx, userID, otherID); err != nil {
		return err
	} else if blocked {
		return ErrNotFriends
	}
	friends, err := areFriendsTx(ctx, tx, userID, otherID)
	if err != nil {
		return err
	}
	if !friends {
		return ErrNotFriends
	}
	return nil
}

// createDirectSpaceTx makes the Space behind a DM: owned by its starter, with every person as a
// member. It never counts against Space limits and never shows among anyone's Spaces.
func createDirectSpaceTx(ctx context.Context, tx *sql.Tx, starterID string, otherIDs []string, pairKey string) (string, error) {
	spaceID := "space_" + uuid.NewString()
	domainID := "sd_" + uuid.NewString()
	if _, err := tx.ExecContext(ctx, `INSERT INTO security_domains(id,kind,owner_user_id,space_id) VALUES($1,'space',$2,$3)`, domainID, starterID, spaceID); err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO spaces(id,owner_user_id,name,security_domain_id,is_default,kind,direct_key)
		VALUES($1,$2,$3,$4,false,'direct',NULLIF($5,''))`, spaceID, starterID, directSpaceName, domainID, pairKey); err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO space_storage_usage(space_id) VALUES($1)`, spaceID); err != nil {
		return "", err
	}
	if err := addSpaceMembershipTx(ctx, tx, spaceID, starterID, "owner"); err != nil {
		return "", err
	}
	for _, otherID := range otherIDs {
		if err := addSpaceMembershipTx(ctx, tx, spaceID, otherID, "member"); err != nil {
			return "", err
		}
	}
	if err := ensureEveryoneRoleTx(ctx, tx, spaceID); err != nil {
		return "", err
	}
	if _, err := recordSpaceEventTx(ctx, tx, spaceID, starterID, "direct.created", spaceID, map[string]any{}); err != nil {
		return "", err
	}
	return spaceID, nil
}

// retireDirectSpaceTx deletes a DM for everyone in it: they lose it now, and the purge worker
// removes its messages and files on its next pass.
func retireDirectSpaceTx(ctx context.Context, tx *sql.Tx, spaceID, actorID string) error {
	rows, err := tx.QueryContext(ctx, `SELECT user_id FROM space_members WHERE space_id=$1`, spaceID)
	if err != nil {
		return err
	}
	memberIDs := []string{}
	for rows.Next() {
		var memberID string
		if err := rows.Scan(&memberID); err != nil {
			rows.Close()
			return err
		}
		memberIDs = append(memberIDs, memberID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if err := purgeSpaceMembersTx(ctx, tx, spaceID, memberIDs); err != nil {
		return err
	}
	if err := notifySpaceControlTx(ctx, tx, map[string]any{"type": "space.deleted", "space_id": spaceID, "user_ids": memberIDs}); err != nil {
		return err
	}
	if err := revokeCodeSessionAccessForSpaceTx(ctx, tx, spaceID); err != nil {
		return err
	}
	if err := revokeNoteAccessForSpaceTx(ctx, tx, spaceID); err != nil {
		return err
	}
	if _, err := recordSpaceEventTx(ctx, tx, spaceID, actorID, "direct.deleted", spaceID, map[string]any{"user_ids": memberIDs}); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE spaces SET lifecycle_state='pending_deletion',deletion_requested_at=NOW(),permanent_delete_after=NOW(),updated_at=NOW() WHERE id=$1`, spaceID)
	return err
}

// deletePairDirectTx deletes the two people's one-to-one DM, if they have one.
func deletePairDirectTx(ctx context.Context, tx *sql.Tx, actorID, otherID string) error {
	var spaceID string
	err := tx.QueryRowContext(ctx, `SELECT id FROM spaces WHERE kind='direct' AND direct_key=$1 AND lifecycle_state='active' FOR UPDATE`, directPairKey(actorID, otherID)).Scan(&spaceID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return retireDirectSpaceTx(ctx, tx, spaceID, actorID)
}

// requireDirectMemberTx returns the DM's pair key ("" for a group) once the caller is one of its
// people; anyone else gets not found.
func requireDirectMemberTx(ctx context.Context, tx *sql.Tx, userID, spaceID string) (string, string, error) {
	var pairKey, role string
	err := tx.QueryRowContext(ctx, `SELECT COALESCE(s.direct_key,''),m.role FROM spaces s JOIN space_members m ON m.space_id=s.id AND m.user_id=$2
		WHERE s.id=$1 AND s.kind='direct' AND s.lifecycle_state='active' FOR UPDATE OF s`, spaceID, userID).Scan(&pairKey, &role)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", ErrSpaceNotFound
	}
	return pairKey, role, err
}

// OpenDirect finds or starts the one-to-one DM between the caller and a friend.
func (db *Database) OpenDirect(ctx context.Context, userID, otherID string) (*DirectConversation, error) {
	var spaceID string
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		key := directPairKey(userID, otherID)
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "dms:pair:"+key); err != nil {
			return err
		}
		err := tx.QueryRowContext(ctx, `SELECT id FROM spaces WHERE kind='direct' AND direct_key=$1 AND lifecycle_state='active'`, key).Scan(&spaceID)
		if err == nil {
			// An existing DM opens for either of them, even one that is read-only now.
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err := requireFriendsTx(ctx, tx, userID, otherID); err != nil {
			return err
		}
		spaceID, err = createDirectSpaceTx(ctx, tx, userID, []string{otherID}, key)
		return err
	})
	if err != nil {
		return nil, err
	}
	return db.Direct(ctx, userID, spaceID)
}

// CreateGroupDirect starts a group DM with two to nine of the caller's friends.
func (db *Database) CreateGroupDirect(ctx context.Context, userID string, otherIDs []string) (*DirectConversation, error) {
	others := uniqueSpaceIDs(otherIDs)
	for index, id := range others {
		if id == userID {
			others = append(others[:index], others[index+1:]...)
			break
		}
	}
	if len(others) < 2 || len(others)+1 > maxDirectPeople {
		return nil, ErrSpaceInvalid
	}
	var spaceID string
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		for _, otherID := range others {
			if err := requireFriendsTx(ctx, tx, userID, otherID); err != nil {
				return err
			}
		}
		var err error
		spaceID, err = createDirectSpaceTx(ctx, tx, userID, others, "")
		return err
	})
	if err != nil {
		return nil, err
	}
	return db.Direct(ctx, userID, spaceID)
}

// AddDirectMembers adds the caller's friends to a group DM. Adding people to a one-to-one DM starts
// a new group with both of its people and the new ones, as before; the one-to-one stays.
func (db *Database) AddDirectMembers(ctx context.Context, userID, spaceID string, otherIDs []string) (*DirectConversation, error) {
	adding := uniqueSpaceIDs(otherIDs)
	if len(adding) == 0 {
		return nil, ErrSpaceInvalid
	}
	resultID := spaceID
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		pairKey, _, err := requireDirectMemberTx(ctx, tx, userID, spaceID)
		if err != nil {
			return err
		}
		current := []string{}
		rows, err := tx.QueryContext(ctx, `SELECT user_id FROM space_members WHERE space_id=$1`, spaceID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var memberID string
			if err := rows.Scan(&memberID); err != nil {
				rows.Close()
				return err
			}
			current = append(current, memberID)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		isMember := map[string]bool{}
		for _, id := range current {
			isMember[id] = true
		}
		fresh := []string{}
		for _, id := range adding {
			if !isMember[id] {
				fresh = append(fresh, id)
			}
		}
		if len(fresh) == 0 {
			return nil
		}
		if len(current)+len(fresh) > maxDirectPeople {
			return ErrSpaceInvalid
		}
		for _, id := range fresh {
			if err := requireFriendsTx(ctx, tx, userID, id); err != nil {
				return err
			}
		}
		if pairKey != "" {
			others := []string{}
			for _, id := range current {
				if id != userID {
					others = append(others, id)
				}
			}
			resultID, err = createDirectSpaceTx(ctx, tx, userID, append(others, fresh...), "")
			return err
		}
		for _, id := range fresh {
			if err := addSpaceMembershipTx(ctx, tx, spaceID, id, "member"); err != nil {
				return err
			}
		}
		_, err = recordSpaceEventTx(ctx, tx, spaceID, userID, "direct.updated", spaceID, map[string]any{})
		return err
	})
	if err != nil {
		return nil, err
	}
	return db.Direct(ctx, userID, resultID)
}

// LeaveDirect takes the caller out of a group DM. When its starter leaves, the person who has been
// in it longest takes it over; when the last person leaves, it is deleted.
func (db *Database) LeaveDirect(ctx context.Context, userID, spaceID string) error {
	return db.SpaceTx(ctx, func(tx *sql.Tx) error {
		pairKey, role, err := requireDirectMemberTx(ctx, tx, userID, spaceID)
		if err != nil {
			return err
		}
		if pairKey != "" {
			// A one-to-one DM isn't left, only deleted.
			return ErrSpaceInvalid
		}
		var next string
		err = tx.QueryRowContext(ctx, `SELECT user_id FROM space_members WHERE space_id=$1 AND user_id<>$2 ORDER BY joined_at,user_id LIMIT 1`, spaceID, userID).Scan(&next)
		if errors.Is(err, sql.ErrNoRows) {
			return retireDirectSpaceTx(ctx, tx, spaceID, userID)
		}
		if err != nil {
			return err
		}
		if err := purgeSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		if role == "owner" {
			if _, err := tx.ExecContext(ctx, `UPDATE space_members SET role='owner' WHERE space_id=$1 AND user_id=$2`, spaceID, next); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE spaces SET owner_user_id=$2,updated_at=NOW() WHERE id=$1`, spaceID, next); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM space_members WHERE space_id=$1 AND user_id=$2`, spaceID, userID); err != nil {
			return err
		}
		if _, err := recordSpaceEventTx(ctx, tx, spaceID, userID, "direct.updated", spaceID, map[string]any{}); err != nil {
			return err
		}
		return notifySpaceControlTx(ctx, tx, map[string]any{"type": "member.left", "space_id": spaceID, "user_ids": []string{userID}})
	})
}

// DeleteDirect deletes a one-to-one DM and everything in it, for both people. A group DM is left
// instead.
func (db *Database) DeleteDirect(ctx context.Context, userID, spaceID string) error {
	return db.SpaceTx(ctx, func(tx *sql.Tx) error {
		pairKey, _, err := requireDirectMemberTx(ctx, tx, userID, spaceID)
		if err != nil {
			return err
		}
		if pairKey == "" {
			return ErrSpaceInvalid
		}
		return retireDirectSpaceTx(ctx, tx, spaceID, userID)
	})
}

// Directs lists the caller's DMs, the most recently active first.
func (db *Database) Directs(ctx context.Context, userID string) ([]DirectConversation, error) {
	items := []DirectConversation{}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		ids := []string{}
		rows, err := tx.QueryContext(ctx, `SELECT s.id FROM spaces s JOIN space_members m ON m.space_id=s.id AND m.user_id=$1
			WHERE s.kind='direct' AND s.lifecycle_state='active'
			ORDER BY COALESCE((SELECT max(created_at) FROM space_messages sm WHERE sm.space_id=s.id),s.created_at) DESC`, userID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		for _, id := range ids {
			item, err := directTx(ctx, tx, userID, id)
			if err != nil {
				return err
			}
			items = append(items, *item)
		}
		return nil
	})
	return items, err
}

// Direct returns one of the caller's DMs.
func (db *Database) Direct(ctx context.Context, userID, spaceID string) (*DirectConversation, error) {
	var out *DirectConversation
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		var err error
		out, err = directTx(ctx, tx, userID, spaceID)
		return err
	})
	return out, err
}

func directTx(ctx context.Context, tx *sql.Tx, userID, spaceID string) (*DirectConversation, error) {
	out := &DirectConversation{ID: spaceID, Members: []Person{}}
	var pairKey string
	var readSeq int64
	err := tx.QueryRowContext(ctx, `SELECT COALESCE(s.direct_key,''),s.created_at,s.updated_at,m.read_message_seq
		FROM spaces s JOIN space_members m ON m.space_id=s.id AND m.user_id=$2
		WHERE s.id=$1 AND s.kind='direct' AND s.lifecycle_state='active'`, spaceID, userID).Scan(&pairKey, &out.CreatedAt, &out.UpdatedAt, &readSeq)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceNotFound
	}
	if err != nil {
		return nil, err
	}
	out.Kind = "group"
	if pairKey != "" {
		out.Kind = "direct"
	}
	rows, err := tx.QueryContext(ctx, `SELECT u.id,u.username,u.avatar_version,m.joined_at FROM space_members m JOIN users u ON u.id=m.user_id
		WHERE m.space_id=$1 ORDER BY m.joined_at,u.id`, spaceID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var person Person
		if err := rows.Scan(&person.UserID, &person.Username, &person.AvatarVersion, &person.Since); err != nil {
			rows.Close()
			return nil, err
		}
		out.Members = append(out.Members, person)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	var last sql.NullTime
	if err := tx.QueryRowContext(ctx, `SELECT max(created_at) FROM space_messages WHERE space_id=$1 AND conversation_id IS NULL`, spaceID).Scan(&last); err != nil {
		return nil, err
	}
	if last.Valid {
		out.LastMessageAt = &last.Time
	}
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM (SELECT 1 FROM space_messages
		WHERE space_id=$1 AND conversation_id IS NULL AND thread_root_id IS NULL AND seq>$2 AND sender_user_id<>$3 LIMIT 100) unread`,
		spaceID, readSeq, userID).Scan(&out.UnreadCount); err != nil {
		return nil, err
	}
	if pairKey != "" {
		for _, member := range out.Members {
			if member.UserID == userID {
				continue
			}
			friends, err := areFriendsTx(ctx, tx, userID, member.UserID)
			if err != nil {
				return nil, err
			}
			blocked, err := blockedEitherWayTx(ctx, tx, userID, member.UserID)
			if err != nil {
				return nil, err
			}
			out.ReadOnly = !friends || blocked
		}
	}
	return out, nil
}

// requireDirectWritableTx refuses a message in a one-to-one DM whose two people aren't friends.
// Anything that isn't a DM passes.
func requireDirectWritableTx(ctx context.Context, tx *sql.Tx, userID, spaceID string) error {
	var pairKey string
	err := tx.QueryRowContext(ctx, `SELECT COALESCE(direct_key,'') FROM spaces WHERE id=$1 AND kind='direct'`, spaceID).Scan(&pairKey)
	if errors.Is(err, sql.ErrNoRows) || pairKey == "" {
		return nil
	}
	if err != nil {
		return err
	}
	for _, id := range splitPairKey(pairKey) {
		if id != userID {
			return requireFriendsTx(ctx, tx, userID, id)
		}
	}
	return nil
}

func splitPairKey(key string) []string {
	for index := 0; index < len(key); index++ {
		if key[index] == ':' {
			return []string{key[:index], key[index+1:]}
		}
	}
	return []string{key}
}
