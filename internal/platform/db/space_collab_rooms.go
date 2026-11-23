package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/misty-org/misty-server/internal/spaces"
)

const (
	CollabPartyApp  = "app-room"
	CollabPartyFile = "file-room"
)

type CollabRoomRecord struct {
	Party       string
	Room        string
	SpaceID     string
	ResourceID  string
	AppID       string
	ItemID      string
	UserID      string
	Permissions []string
}

type CollabRoomControlCommand struct {
	ID         string
	Party      string
	Room       string
	ResourceID string
	Command    string
	Payload    []byte
	Attempts   int
}

const collabRoomControlHead = `o.delivered_at IS NULL AND NOT EXISTS(
	SELECT 1 FROM space_collab_room_control_outbox earlier
	WHERE earlier.party=o.party AND earlier.room=o.room AND earlier.delivered_at IS NULL
	AND (earlier.created_at,earlier.id)<(o.created_at,o.id))`

type collabRoomTarget struct {
	party, room, resourceID string
	userIDs                 []string
}

func (db *Database) RecordCollabRoom(ctx context.Context, record CollabRoomRecord) error {
	if (record.Party != CollabPartyApp && record.Party != CollabPartyFile) || record.Room == "" ||
		record.SpaceID == "" || record.ResourceID == "" || record.UserID == "" || record.AppID == "" {
		return ErrSpaceInvalid
	}
	return db.SpaceTx(ctx, func(tx *sql.Tx) error {
		var found int
		err := tx.QueryRowContext(ctx, `SELECT 1 FROM spaces WHERE id=$1 AND lifecycle_state='active' FOR SHARE`,
			record.SpaceID).Scan(&found)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrSpaceForbidden
		}
		if err != nil {
			return err
		}
		err = tx.QueryRowContext(ctx, `SELECT 1 FROM space_members WHERE space_id=$1 AND user_id=$2 FOR SHARE`,
			record.SpaceID, record.UserID).Scan(&found)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrSpaceForbidden
		}
		if err != nil {
			return err
		}
		var permissions []string
		err = tx.QueryRowContext(ctx, `SELECT permissions FROM space_apps WHERE space_id=$1 AND app_id=$2 FOR SHARE`,
			record.SpaceID, record.AppID).Scan(pq.Array(&permissions))
		if errors.Is(err, sql.ErrNoRows) {
			return ErrSpaceNotFound
		}
		if err != nil {
			return err
		}
		if !holdsAppPermissions(permissions, record.Permissions...) {
			return ErrSpaceForbidden
		}
		var roomApp, roomItem any
		if record.Party == CollabPartyApp || record.ResourceID != record.SpaceID+":"+record.ItemID {
			roomApp = record.AppID
		}
		if record.ItemID != "" {
			roomItem = record.ItemID
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO space_collab_rooms(party,room,space_id,resource_id,app_id,item_id)
			VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT (party,room) DO NOTHING`,
			record.Party, record.Room, record.SpaceID, record.ResourceID, roomApp, roomItem); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO space_collab_room_users(party,room,user_id,app_id,space_id)
			VALUES($1,$2,$3,$4,$5) ON CONFLICT (party,room,user_id,app_id) DO NOTHING`,
			record.Party, record.Room, record.UserID, record.AppID, record.SpaceID)
		return err
	})
}

func holdsAppPermissions(held []string, required ...string) bool {
	for _, want := range required {
		found := false
		for _, permission := range held {
			if permission == want {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func enqueueCollabRoomControlTx(ctx context.Context, tx *sql.Tx, spaceID string, target collabRoomTarget, command string) error {
	payload := map[string]any{}
	if target.userIDs != nil {
		payload["user_ids"] = target.userIDs
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO space_collab_room_control_outbox(id,space_id,party,room,resource_id,command,payload)
		VALUES($1,$2,$3,$4,$5,$6,$7)`,
		"roomctl_"+uuid.NewString(), spaceID, target.party, target.room, target.resourceID, command, raw)
	return err
}

func collabRoomTargetsTx(ctx context.Context, tx *sql.Tx, query string, args ...any) ([]collabRoomTarget, error) {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	targets := []collabRoomTarget{}
	for rows.Next() {
		var target collabRoomTarget
		var userIDs pq.StringArray
		if err := rows.Scan(&target.party, &target.room, &target.resourceID, &userIDs); err != nil {
			return nil, err
		}
		if userIDs != nil {
			target.userIDs = []string(userIDs)
		}
		targets = append(targets, target)
	}
	return targets, rows.Err()
}

func enqueueCollabRoomTargetsTx(ctx context.Context, tx *sql.Tx, spaceID, command string, targets []collabRoomTarget) error {
	for _, target := range targets {
		if err := enqueueCollabRoomControlTx(ctx, tx, spaceID, target, command); err != nil {
			return err
		}
	}
	return nil
}

func revokeCollabRoomsForUserTx(ctx context.Context, tx *sql.Tx, spaceID, userID string) error {
	if _, err := tx.ExecContext(ctx, `SELECT 1 FROM space_members WHERE space_id=$1 AND user_id=$2 FOR UPDATE`,
		spaceID, userID); err != nil {
		return err
	}
	targets, err := collabRoomTargetsTx(ctx, tx, `SELECT r.party,r.room,r.resource_id,ARRAY[$2::text]
		FROM space_collab_rooms r
		WHERE r.space_id=$1 AND EXISTS(SELECT 1 FROM space_collab_room_users u
			WHERE u.party=r.party AND u.room=r.room AND u.user_id=$2)
		ORDER BY r.party,r.room`, spaceID, userID)
	if err != nil {
		return err
	}
	if err := enqueueCollabRoomTargetsTx(ctx, tx, spaceID, "disconnect", targets); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM space_collab_room_users WHERE space_id=$1 AND user_id=$2`, spaceID, userID)
	return err
}

func fileRoomWritersTx(ctx context.Context, tx *sql.Tx, spaceID string) (map[string]bool, error) {
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT user_id FROM space_collab_room_users
		WHERE space_id=$1 AND party=$2 ORDER BY user_id`, spaceID, CollabPartyFile)
	if err != nil {
		return nil, err
	}
	userIDs := []string{}
	for rows.Next() {
		var userID string
		if err := rows.Scan(&userID); err != nil {
			rows.Close()
			return nil, err
		}
		userIDs = append(userIDs, userID)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	writers := map[string]bool{}
	for _, userID := range userIDs {
		can, err := hasSpacePermissionTx(ctx, tx, userID, spaceID, spaces.PermissionDriveWrite)
		if errors.Is(err, ErrSpaceForbidden) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if can {
			writers[userID] = true
		}
	}
	return writers, nil
}

func revokeLostFileRoomWritersTx(ctx context.Context, tx *sql.Tx, spaceID string, before map[string]bool) error {
	if len(before) == 0 {
		return nil
	}
	after, err := fileRoomWritersTx(ctx, tx, spaceID)
	if err != nil {
		return err
	}
	lost := []string{}
	for userID := range before {
		if !after[userID] {
			lost = append(lost, userID)
		}
	}
	if len(lost) == 0 {
		return nil
	}
	targets, err := collabRoomTargetsTx(ctx, tx, `SELECT r.party,r.room,r.resource_id,array_agg(DISTINCT u.user_id ORDER BY u.user_id)
		FROM space_collab_rooms r JOIN space_collab_room_users u ON u.party=r.party AND u.room=r.room
		WHERE r.space_id=$1 AND r.party=$2 AND u.user_id=ANY($3)
		GROUP BY r.party,r.room,r.resource_id ORDER BY r.room`, spaceID, CollabPartyFile, pq.Array(lost))
	if err != nil {
		return err
	}
	return enqueueCollabRoomTargetsTx(ctx, tx, spaceID, "disconnect", targets)
}

func disconnectAppFileRoomUsersTx(ctx context.Context, tx *sql.Tx, spaceID, appID string) error {
	targets, err := collabRoomTargetsTx(ctx, tx, `SELECT r.party,r.room,r.resource_id,array_agg(DISTINCT u.user_id ORDER BY u.user_id)
		FROM space_collab_rooms r JOIN space_collab_room_users u ON u.party=r.party AND u.room=r.room
		WHERE r.space_id=$1 AND r.party=$2 AND u.app_id=$3
		AND (r.app_id=$3 OR NOT EXISTS(SELECT 1 FROM space_collab_room_users other
			WHERE other.party=r.party AND other.room=r.room AND other.user_id=u.user_id AND other.app_id<>$3))
		GROUP BY r.party,r.room,r.resource_id ORDER BY r.room`, spaceID, CollabPartyFile, appID)
	if err != nil {
		return err
	}
	if err := enqueueCollabRoomTargetsTx(ctx, tx, spaceID, "disconnect", targets); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM space_collab_room_users WHERE space_id=$1 AND party=$2 AND app_id=$3`,
		spaceID, CollabPartyFile, appID)
	return err
}

func revokeAppRoomAccessTx(ctx context.Context, tx *sql.Tx, spaceID, appID string, before, after []string) error {
	lostRooms := holdsAppPermissions(before, "rooms") && !holdsAppPermissions(after, "rooms")
	lostFiles := holdsAppPermissions(before, "rooms", "files") && !holdsAppPermissions(after, "rooms", "files")
	if lostRooms {
		targets, err := collabRoomTargetsTx(ctx, tx, `SELECT party,room,resource_id,NULL::text[]
			FROM space_collab_rooms WHERE space_id=$1 AND app_id=$2 AND party=$3 ORDER BY room`,
			spaceID, appID, CollabPartyApp)
		if err != nil {
			return err
		}
		if err := enqueueCollabRoomTargetsTx(ctx, tx, spaceID, "disconnect", targets); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM space_collab_room_users WHERE space_id=$1 AND party=$2 AND app_id=$3`,
			spaceID, CollabPartyApp, appID); err != nil {
			return err
		}
	}
	if lostFiles {
		return disconnectAppFileRoomUsersTx(ctx, tx, spaceID, appID)
	}
	return nil
}

func purgeAppRoomsTx(ctx context.Context, tx *sql.Tx, spaceID, appID string) error {
	targets, err := collabRoomTargetsTx(ctx, tx, `SELECT party,room,resource_id,NULL::text[]
		FROM space_collab_rooms WHERE space_id=$1 AND app_id=$2 AND party=$3 ORDER BY room`,
		spaceID, appID, CollabPartyApp)
	if err != nil {
		return err
	}
	if err := enqueueCollabRoomTargetsTx(ctx, tx, spaceID, "purge", targets); err != nil {
		return err
	}
	fileRooms, err := collabRoomTargetsTx(ctx, tx, `SELECT party,room,resource_id,NULL::text[]
		FROM space_collab_rooms WHERE space_id=$1 AND app_id=$2 AND party=$3 ORDER BY room`,
		spaceID, appID, CollabPartyFile)
	if err != nil {
		return err
	}
	if err := enqueueCollabRoomTargetsTx(ctx, tx, spaceID, "disconnect", fileRooms); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM space_collab_rooms WHERE space_id=$1 AND app_id=$2 AND party=$3`,
		spaceID, appID, CollabPartyApp); err != nil {
		return err
	}
	return disconnectAppFileRoomUsersTx(ctx, tx, spaceID, appID)
}

func beginCollabRoomSpacePurgeTx(ctx context.Context, tx *sql.Tx, spaceID string) (int, error) {
	targets, err := collabRoomTargetsTx(ctx, tx, `SELECT r.party,r.room,r.resource_id,NULL::text[]
		FROM space_collab_rooms r WHERE r.space_id=$1
		AND NOT EXISTS(SELECT 1 FROM space_collab_room_control_outbox o
			WHERE o.space_id=r.space_id AND o.party=r.party AND o.room=r.room AND o.command='purge')
		ORDER BY r.party,r.room`, spaceID)
	if err != nil {
		return 0, err
	}
	if err := enqueueCollabRoomTargetsTx(ctx, tx, spaceID, "purge", targets); err != nil {
		return 0, err
	}
	var waiting int
	err = tx.QueryRowContext(ctx, `SELECT count(*) FROM space_collab_rooms r WHERE r.space_id=$1
		AND NOT EXISTS(SELECT 1 FROM space_collab_room_control_outbox o
			WHERE o.space_id=r.space_id AND o.party=r.party AND o.room=r.room AND o.command='purge'
			AND o.delivered_at IS NOT NULL)`, spaceID).Scan(&waiting)
	return waiting, err
}

func (db *Database) PendingCollabRoomControlCommands(ctx context.Context, limit int) ([]CollabRoomControlCommand, error) {
	if limit < 1 || limit > 200 {
		limit = 50
	}
	commands := []CollabRoomControlCommand{}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `UPDATE space_collab_room_control_outbox
			SET attempts=attempts+1,next_attempt_at=NOW()+(LEAST(attempts+1,6)*INTERVAL '10 seconds')
			WHERE id IN (
				SELECT o.id FROM space_collab_room_control_outbox o
				WHERE `+collabRoomControlHead+` AND o.next_attempt_at<=NOW()
				ORDER BY o.next_attempt_at,o.created_at
				FOR UPDATE SKIP LOCKED LIMIT $1)
			RETURNING id,party,room,resource_id,command,payload,attempts`, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var command CollabRoomControlCommand
			if err := rows.Scan(&command.ID, &command.Party, &command.Room, &command.ResourceID,
				&command.Command, &command.Payload, &command.Attempts); err != nil {
				return err
			}
			commands = append(commands, command)
		}
		return rows.Err()
	})
	return commands, err
}

func (db *Database) MarkCollabRoomControlDelivered(ctx context.Context, commandID string) error {
	return db.SpaceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE space_collab_room_control_outbox SET delivered_at=NOW(),last_error=''
			WHERE id=$1 AND delivered_at IS NULL`, commandID)
		return err
	})
}

func (db *Database) AbandonCollabRoomControlCommand(ctx context.Context, commandID, reason string) error {
	if len(reason) > 500 {
		reason = reason[:500]
	}
	return db.SpaceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE space_collab_room_control_outbox SET delivered_at=NOW(),last_error=$1
			WHERE id=$2 AND delivered_at IS NULL`, reason, commandID)
		return err
	})
}

func (db *Database) MarkCollabRoomControlFailed(ctx context.Context, commandID, reason string) error {
	if len(reason) > 500 {
		reason = reason[:500]
	}
	return db.SpaceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE space_collab_room_control_outbox SET last_error=$1
			WHERE id=$2 AND delivered_at IS NULL`, reason, commandID)
		return err
	})
}
