package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

type LibraryAssetStackMember struct {
	ItemID           string `json:"item_id"`
	Role             string `json:"role"`
	Position         int    `json:"position"`
	DisplayName      string `json:"display_name,omitempty"`
	OriginalFilename string `json:"original_filename,omitempty"`
	MIMEType         string `json:"mime_type,omitempty"`
}

type LibraryAssetStack struct {
	ID              string                    `json:"id"`
	SpaceID         string                    `json:"space_id"`
	Kind            string                    `json:"kind"`
	Title           string                    `json:"title"`
	CoverItemID     string                    `json:"cover_item_id"`
	MotionItemID    string                    `json:"motion_item_id,omitempty"`
	Effect          string                    `json:"effect"`
	CreatedByUserID string                    `json:"created_by_user_id"`
	LifecycleState  string                    `json:"lifecycle_state"`
	Version         int64                     `json:"version"`
	CreatedAt       time.Time                 `json:"created_at"`
	UpdatedAt       time.Time                 `json:"updated_at"`
	Members         []LibraryAssetStackMember `json:"members"`
}

type CreateLibraryAssetStack struct {
	Kind         string                    `json:"kind"`
	Title        string                    `json:"title"`
	CoverItemID  string                    `json:"cover_item_id"`
	MotionItemID string                    `json:"motion_item_id"`
	Members      []LibraryAssetStackMember `json:"members"`
}

func (db *Database) LibraryAssetStacks(ctx context.Context, userID, spaceID string) ([]LibraryAssetStack, error) {
	out := []LibraryAssetStack{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionLibraryView); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT s.id,s.space_id,s.kind,s.title,s.cover_item_id,COALESCE(s.motion_item_id,''),s.effect,s.created_by_user_id,s.lifecycle_state,s.version,s.created_at,s.updated_at FROM space_library_asset_stacks s WHERE s.space_id=$1 AND s.lifecycle_state='ready' AND NOT EXISTS(SELECT 1 FROM space_library_asset_stack_members m JOIN space_library_items i ON i.id=m.space_library_item_id WHERE m.stack_id=s.id AND (i.lifecycle_state<>'ready' OR i.hidden)) ORDER BY s.created_at`, spaceID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var stack LibraryAssetStack
			if err := rows.Scan(&stack.ID, &stack.SpaceID, &stack.Kind, &stack.Title, &stack.CoverItemID, &stack.MotionItemID, &stack.Effect, &stack.CreatedByUserID, &stack.LifecycleState, &stack.Version, &stack.CreatedAt, &stack.UpdatedAt); err != nil {
				return err
			}
			out = append(out, stack)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		for index := range out {
			members, err := libraryAssetStackMembersTx(ctx, tx, out[index].ID)
			if err != nil {
				return err
			}
			out[index].Members = members
		}
		return nil
	})
	return out, err
}

func (db *Database) CreateLibraryAssetStack(ctx context.Context, userID, spaceID string, input CreateLibraryAssetStack) (*LibraryAssetStack, error) {
	input.Kind = strings.TrimSpace(input.Kind)
	input.Title = strings.TrimSpace(input.Title)
	if !map[string]bool{"live_photo": true, "raw_pair": true, "burst": true}[input.Kind] || len([]rune(input.Title)) > 160 || input.CoverItemID == "" || len(input.Members) < 2 || len(input.Members) > 100 {
		return nil, ErrLibraryInvalid
	}
	if input.Kind != "burst" && len(input.Members) != 2 || input.Kind == "burst" && len(input.Members) < 2 {
		return nil, ErrLibraryInvalid
	}
	seenItems, seenPositions := map[string]bool{}, map[int]bool{}
	for _, member := range input.Members {
		if member.ItemID == "" || member.Position < 0 || seenItems[member.ItemID] || seenPositions[member.Position] {
			return nil, ErrLibraryInvalid
		}
		seenItems[member.ItemID], seenPositions[member.Position] = true, true
	}
	if !seenItems[input.CoverItemID] || input.MotionItemID != "" && !seenItems[input.MotionItemID] {
		return nil, ErrLibraryInvalid
	}
	stackID := "asset_stack_" + uuid.NewString()
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionLibraryEdit); err != nil {
			return err
		}
		if err := validateLibraryAssetStackTx(ctx, tx, spaceID, input); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "library:asset-stack:"+spaceID); err != nil {
			return err
		}
		var overlap bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM space_library_asset_stack_members m JOIN space_library_asset_stacks s ON s.id=m.stack_id WHERE s.space_id=$1 AND s.kind=$2 AND s.lifecycle_state='ready' AND m.space_library_item_id=ANY($3))`, spaceID, input.Kind, pq.Array(itemIDsForStack(input.Members))).Scan(&overlap); err != nil {
			return err
		}
		if overlap {
			return ErrLibraryConflict
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO space_library_asset_stacks(id,space_id,kind,title,cover_item_id,motion_item_id,created_by_user_id) VALUES($1,$2,$3,$4,$5,NULLIF($6,''),$7)`, stackID, spaceID, input.Kind, input.Title, input.CoverItemID, input.MotionItemID, userID); err != nil {
			return err
		}
		for _, member := range input.Members {
			if _, err := tx.ExecContext(ctx, `INSERT INTO space_library_asset_stack_members(stack_id,space_library_item_id,role,position) VALUES($1,$2,$3,$4)`, stackID, member.ItemID, member.Role, member.Position); err != nil {
				return err
			}
		}
		return insertLibraryAuditTx(ctx, tx, spaceID, "", userID, "library.asset_stack.created", "asset_stack", stackID, "success", map[string]any{"kind": input.Kind, "item_count": len(input.Members)})
	})
	if err != nil {
		return nil, err
	}
	return db.libraryAssetStack(ctx, userID, spaceID, stackID)
}

func (db *Database) DeleteLibraryAssetStack(ctx context.Context, userID, spaceID, stackID string, version int64) error {
	if stackID == "" || version < 1 {
		return ErrLibraryInvalid
	}
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionLibraryEdit); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE space_library_asset_stacks SET lifecycle_state='deleted',version=version+1,updated_at=NOW() WHERE id=$1 AND space_id=$2 AND lifecycle_state='ready' AND version=$3`, stackID, spaceID, version)
		if err != nil {
			return err
		}
		if count, _ := result.RowsAffected(); count == 0 {
			return ErrLibraryConflict
		}
		return insertLibraryAuditTx(ctx, tx, spaceID, "", userID, "library.asset_stack.deleted", "asset_stack", stackID, "success", nil)
	})
}

func (db *Database) UpdateLibraryAssetStack(ctx context.Context, userID, spaceID, stackID string, version int64, title, coverItemID, effect string) (*LibraryAssetStack, error) {
	title = strings.TrimSpace(title)
	effect = strings.TrimSpace(effect)
	if stackID == "" || version < 1 || coverItemID == "" || len([]rune(title)) > 160 || !map[string]bool{"still": true, "loop": true, "bounce": true, "long_exposure": true}[effect] {
		return nil, ErrLibraryInvalid
	}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionLibraryEdit); err != nil {
			return err
		}
		var valid, livePhoto bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM space_library_asset_stack_members m JOIN space_library_asset_stacks s ON s.id=m.stack_id WHERE s.id=$1 AND s.space_id=$2 AND s.lifecycle_state='ready' AND m.space_library_item_id=$3 AND m.role IN ('still','alternate','burst_frame')),EXISTS(SELECT 1 FROM space_library_asset_stacks WHERE id=$1 AND space_id=$2 AND kind='live_photo' AND lifecycle_state='ready')`, stackID, spaceID, coverItemID).Scan(&valid, &livePhoto); err != nil {
			return err
		}
		if !valid {
			return ErrLibraryInvalid
		}
		if !livePhoto && effect != "still" {
			return ErrLibraryInvalid
		}
		result, err := tx.ExecContext(ctx, `UPDATE space_library_asset_stacks SET title=$1,cover_item_id=$2,effect=$3,version=version+1,updated_at=NOW() WHERE id=$4 AND space_id=$5 AND lifecycle_state='ready' AND version=$6`, title, coverItemID, effect, stackID, spaceID, version)
		if err != nil {
			return err
		}
		if count, _ := result.RowsAffected(); count == 0 {
			return ErrLibraryConflict
		}
		return insertLibraryAuditTx(ctx, tx, spaceID, "", userID, "library.asset_stack.updated", "asset_stack", stackID, "success", map[string]any{"cover_item_id": coverItemID, "effect": effect})
	})
	if err != nil {
		return nil, err
	}
	return db.libraryAssetStack(ctx, userID, spaceID, stackID)
}

func (db *Database) libraryAssetStack(ctx context.Context, userID, spaceID, stackID string) (*LibraryAssetStack, error) {
	var out LibraryAssetStack
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionLibraryView); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `SELECT id,space_id,kind,title,cover_item_id,COALESCE(motion_item_id,''),effect,created_by_user_id,lifecycle_state,version,created_at,updated_at FROM space_library_asset_stacks WHERE id=$1 AND space_id=$2 AND lifecycle_state='ready'`, stackID, spaceID).Scan(&out.ID, &out.SpaceID, &out.Kind, &out.Title, &out.CoverItemID, &out.MotionItemID, &out.Effect, &out.CreatedByUserID, &out.LifecycleState, &out.Version, &out.CreatedAt, &out.UpdatedAt); err != nil {
			return err
		}
		members, err := libraryAssetStackMembersTx(ctx, tx, stackID)
		out.Members = members
		return err
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrLibraryNotFound
	}
	return &out, err
}

func libraryAssetStackMembersTx(ctx context.Context, tx *sql.Tx, stackID string) ([]LibraryAssetStackMember, error) {
	out := []LibraryAssetStackMember{}
	rows, err := tx.QueryContext(ctx, `SELECT m.space_library_item_id,m.role,m.position,i.display_name,f.original_filename,b.server_detected_mime_type FROM space_library_asset_stack_members m JOIN space_library_items i ON i.id=m.space_library_item_id JOIN library_files f ON f.id=i.file_id JOIN library_blobs b ON b.id=f.blob_id WHERE m.stack_id=$1 AND i.lifecycle_state='ready' AND i.hidden=FALSE ORDER BY m.position`, stackID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var member LibraryAssetStackMember
		if err := rows.Scan(&member.ItemID, &member.Role, &member.Position, &member.DisplayName, &member.OriginalFilename, &member.MIMEType); err != nil {
			return nil, err
		}
		out = append(out, member)
	}
	return out, rows.Err()
}

func validateLibraryAssetStackTx(ctx context.Context, tx *sql.Tx, spaceID string, input CreateLibraryAssetStack) error {
	type itemKind struct{ id, mime, filename string }
	items := map[string]itemKind{}
	rows, err := tx.QueryContext(ctx, `SELECT i.id,b.server_detected_mime_type,f.original_filename FROM space_library_items i JOIN library_files f ON f.id=i.file_id JOIN library_blobs b ON b.id=f.blob_id WHERE i.space_id=$1 AND i.lifecycle_state='ready' AND i.id=ANY($2)`, spaceID, pq.Array(itemIDsForStack(input.Members)))
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var item itemKind
		if err := rows.Scan(&item.id, &item.mime, &item.filename); err != nil {
			return err
		}
		items[item.id] = item
	}
	if err := rows.Err(); err != nil || len(items) != len(input.Members) {
		if err != nil {
			return err
		}
		return ErrLibraryNotFound
	}
	roleCounts := map[string]int{}
	for _, member := range input.Members {
		item := items[member.ItemID]
		roleCounts[member.Role]++
		switch input.Kind {
		case "live_photo":
			if member.Role == "still" && !strings.HasPrefix(item.mime, "image/") || member.Role == "motion" && !strings.HasPrefix(item.mime, "video/") || member.Role != "still" && member.Role != "motion" {
				return ErrLibraryInvalid
			}
		case "raw_pair":
			isRAW := libraryRAWFilename(item.filename)
			if member.Role == "raw" && !isRAW || member.Role == "alternate" && !strings.HasPrefix(item.mime, "image/") || member.Role != "raw" && member.Role != "alternate" {
				return ErrLibraryInvalid
			}
		case "burst":
			if member.Role != "burst_frame" || !strings.HasPrefix(item.mime, "image/") {
				return ErrLibraryInvalid
			}
		}
	}
	if input.Kind == "live_photo" && (roleCounts["still"] != 1 || roleCounts["motion"] != 1 || input.MotionItemID == "" || input.MotionItemID == input.CoverItemID) {
		return ErrLibraryInvalid
	}
	if input.Kind == "raw_pair" && (roleCounts["raw"] != 1 || roleCounts["alternate"] != 1) {
		return ErrLibraryInvalid
	}
	if input.Kind == "burst" && roleCounts["burst_frame"] != len(input.Members) {
		return ErrLibraryInvalid
	}
	return nil
}

func libraryRAWFilename(filename string) bool {
	ext := strings.ToLower(filename)
	for _, suffix := range []string{".dng", ".cr2", ".cr3", ".nef", ".nrw", ".arw", ".srf", ".sr2", ".raf", ".rw2", ".orf", ".pef", ".x3f"} {
		if strings.HasSuffix(ext, suffix) {
			return true
		}
	}
	return false
}

func itemIDsForStack(members []LibraryAssetStackMember) []string {
	out := make([]string, len(members))
	for index, member := range members {
		out[index] = member.ItemID
	}
	return out
}
