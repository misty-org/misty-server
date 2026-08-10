package db

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
)

func (db *Database) LibraryItems(ctx context.Context, userID, spaceID string, query LibraryItemQuery) ([]SpaceLibraryItem, error) {
	if query.Limit < 1 || query.Limit > 200 {
		query.Limit = 100
	}
	query.Search = strings.TrimSpace(query.Search)
	if len([]rune(query.Search)) > 240 {
		return nil, ErrLibraryInvalid
	}
	structuredSearch, err := TestingParseLibrarySearch(query.Search)
	if err != nil {
		return nil, err
	}
	query.Search = structuredSearch.Text
	if structuredSearch.MediaType != "" {
		query.MediaType = structuredSearch.MediaType
	}
	if structuredSearch.Hidden != nil {
		if *structuredSearch.Hidden {
			query.Visibility = "hidden"
		} else {
			query.Visibility = "visible"
		}
	}
	if structuredSearch.DateFrom != nil {
		query.DateFrom = structuredSearch.DateFrom
	}
	if structuredSearch.DateTo != nil {
		query.DateTo = structuredSearch.DateTo
	}
	if query.Direction != "asc" {
		query.Direction = "desc"
	}
	if query.Visibility != "all" && query.Visibility != "hidden" {
		query.Visibility = "visible"
	}
	state := "ready"
	if query.Collection == "recently-deleted" {
		state = "trash"
	}
	sortExpression := "i.added_at"
	subquerySortExpression := "cursor_item.added_at"
	switch query.Sort {
	case "date-captured":
		sortExpression = "COALESCE(i.date_override,f.intrinsic_capture_at,f.original_uploaded_at)"
		subquerySortExpression = "COALESCE(cursor_item.date_override,cursor_file.intrinsic_capture_at,cursor_file.original_uploaded_at)"
	case "name":
		sortExpression = "lower(i.display_name)"
		subquerySortExpression = "lower(cursor_item.display_name)"
	case "size":
		sortExpression = "b.byte_size"
		subquerySortExpression = "cursor_blob.byte_size"
	default:
		query.Sort = "recently-added"
	}
	items := []SpaceLibraryItem{}
	err = db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionLibraryView); err != nil {
			return err
		}
		conditions := []string{"i.space_id=$1", "i.lifecycle_state=$2"}
		if state == "ready" {
			conditions = append(conditions, "NOT EXISTS(SELECT 1 FROM space_library_asset_stack_members stack_member JOIN space_library_asset_stacks asset_stack ON asset_stack.id=stack_member.stack_id WHERE stack_member.space_library_item_id=i.id AND asset_stack.lifecycle_state='ready' AND asset_stack.cover_item_id<>i.id)")
		}
		args := []any{spaceID, state}
		addArgument := func(value any) string {
			args = append(args, value)
			return fmt.Sprintf("$%d", len(args))
		}
		viewerPlaceholder := addArgument(userID)
		conditions = append(conditions, "(i.audience_kind='space' OR EXISTS(SELECT 1 FROM space_conversation_members audience_member WHERE audience_member.conversation_id=i.audience_conversation_id AND audience_member.actor_kind='person' AND audience_member.user_id="+viewerPlaceholder+"))")
		switch query.Visibility {
		case "visible":
			conditions = append(conditions, "i.hidden=FALSE")
		case "hidden":
			conditions = append(conditions, "i.hidden=TRUE")
		}
		if query.Favorite {
			conditions = append(conditions, "i.favorite=TRUE")
		}
		if structuredSearch.Favorite != nil {
			conditions = append(conditions, "i.favorite="+addArgument(*structuredSearch.Favorite))
		}
		for _, tag := range structuredSearch.Tags {
			conditions = append(conditions, "EXISTS(SELECT 1 FROM jsonb_array_elements_text(i.tags) search_tag WHERE lower(search_tag)=lower("+addArgument(tag)+"))")
		}
		if query.Search != "" {
			placeholder := addArgument(query.Search)
			conditions = append(conditions, "(to_tsvector('simple',i.display_name||' '||i.caption||' '||i.tags::text) @@ plainto_tsquery('simple',"+placeholder+") OR to_tsvector('simple',f.original_filename||' '||f.intrinsic_metadata::text) @@ plainto_tsquery('simple',"+placeholder+"))")
		}
		switch query.MediaType {
		case "image", "video", "audio":
			conditions = append(conditions, "b.server_detected_mime_type LIKE "+addArgument(query.MediaType+"/%"))
		case "document":
			conditions = append(conditions, "b.server_detected_mime_type NOT LIKE 'image/%' AND b.server_detected_mime_type NOT LIKE 'video/%' AND b.server_detected_mime_type NOT LIKE 'audio/%'")
		case "selfies", "live-photos", "portraits", "panoramas", "slo-mo", "cinematic", "bursts", "raw", "screenshots", "screen-recordings", "spatial":
			conditions = append(conditions, libraryMediaSubtypeCondition(query.MediaType))
		case "":
		default:
			return ErrLibraryInvalid
		}
		if query.AlbumID != "" {
			conditions = append(conditions, "EXISTS(SELECT 1 FROM space_album_items album_item JOIN space_albums album ON album.id=album_item.album_id WHERE album_item.space_library_item_id=i.id AND album.id="+addArgument(query.AlbumID)+" AND album.space_id=i.space_id)")
		}
		switch query.Utility {
		case "":
		case "recently-viewed":
			recentViewerPlaceholder := addArgument(userID)
			conditions = append(conditions, "EXISTS(SELECT 1 FROM space_library_item_views item_view WHERE item_view.space_id=i.space_id AND item_view.space_library_item_id=i.id AND item_view.user_id="+recentViewerPlaceholder+")")
			sortExpression = "(SELECT item_view.last_viewed_at FROM space_library_item_views item_view WHERE item_view.space_id=i.space_id AND item_view.space_library_item_id=i.id AND item_view.user_id=" + recentViewerPlaceholder + ")"
			subquerySortExpression = "(SELECT item_view.last_viewed_at FROM space_library_item_views item_view WHERE item_view.space_id=cursor_item.space_id AND item_view.space_library_item_id=cursor_item.id AND item_view.user_id=" + recentViewerPlaceholder + ")"
		case "recently-edited":
			conditions = append(conditions, "i.current_edit_version_id IS NOT NULL")
			sortExpression, subquerySortExpression = "i.updated_at", "cursor_item.updated_at"
		case "recently-shared":
			conditions = append(conditions, "(EXISTS(SELECT 1 FROM space_library_grants grant_record WHERE grant_record.source_space_id=i.space_id AND grant_record.source_item_id=i.id AND grant_record.state='active') OR EXISTS(SELECT 1 FROM space_message_library_references message_reference WHERE message_reference.space_id=i.space_id AND message_reference.space_library_item_id=i.id))")
		case "recently-saved":
			conditions = append(conditions, "EXISTS(SELECT 1 FROM space_message_attachments saved_attachment WHERE saved_attachment.space_id=i.space_id AND saved_attachment.promoted_item_id=i.id)")
			sortExpression, subquerySortExpression = "i.added_at", "cursor_item.added_at"
		case "recovered":
			conditions = append(conditions, "EXISTS(SELECT 1 FROM space_library_audit_events recovery_event WHERE recovery_event.space_id=i.space_id AND recovery_event.target_kind='library_item' AND recovery_event.target_id=i.id AND recovery_event.action='library.item.restored' AND recovery_event.outcome='success')")
			sortExpression, subquerySortExpression = "i.updated_at", "cursor_item.updated_at"
		case "imports":
			conditions = append(conditions, "EXISTS(SELECT 1 FROM space_library_imports import_record WHERE import_record.destination_space_id=i.space_id AND import_record.destination_item_id=i.id AND import_record.state='ready')")
		case "featured":
			conditions = append(conditions, "b.server_detected_mime_type LIKE 'image/%' AND (i.favorite OR EXISTS(SELECT 1 FROM library_derivatives featured_derivative WHERE featured_derivative.space_library_item_id=i.id AND featured_derivative.lifecycle_state='ready' AND featured_derivative.kind='ai_metadata' AND lower(featured_derivative.metadata::text) ~ '(featured|aesthetic|best shot|high quality)'))")
		case "screenshots":
			conditions = append(conditions, "(lower(f.original_filename) LIKE '%screenshot%' OR lower(f.intrinsic_metadata::text) LIKE '%screenshot%')")
		case "documents":
			conditions = append(conditions, "b.server_detected_mime_type NOT LIKE 'image/%' AND b.server_detected_mime_type NOT LIKE 'video/%' AND b.server_detected_mime_type NOT LIKE 'audio/%'")
		case "receipts", "handwriting", "illustrations", "qr-codes":
			keyword := map[string]string{"receipts": "receipt", "handwriting": "handwrit", "illustrations": "illustration", "qr-codes": "qr"}[query.Utility]
			conditions = append(conditions, "EXISTS(SELECT 1 FROM library_derivatives intelligence WHERE intelligence.space_library_item_id=i.id AND intelligence.lifecycle_state='ready' AND intelligence.kind='ai_metadata' AND lower(intelligence.metadata::text) LIKE "+addArgument("%"+keyword+"%")+")")
		default:
			return ErrLibraryInvalid
		}
		if structuredSearch.Album != "" {
			placeholder := addArgument(structuredSearch.Album)
			conditions = append(conditions, "EXISTS(SELECT 1 FROM space_album_items search_album_item JOIN space_albums search_album ON search_album.id=search_album_item.album_id WHERE search_album_item.space_library_item_id=i.id AND search_album.space_id=i.space_id AND (search_album.id="+placeholder+" OR lower(search_album.name)=lower("+placeholder+")))")
		}
		if query.DateFrom != nil {
			conditions = append(conditions, "COALESCE(i.date_override,f.intrinsic_capture_at,f.original_uploaded_at)>="+addArgument(*query.DateFrom))
		}
		if query.DateTo != nil {
			conditions = append(conditions, "COALESCE(i.date_override,f.intrinsic_capture_at,f.original_uploaded_at)<"+addArgument(*query.DateTo))
		}
		if query.After != "" {
			operator := "<"
			if query.Direction == "asc" {
				operator = ">"
			}
			placeholder := addArgument(query.After)
			conditions = append(conditions, fmt.Sprintf("(%s,i.id)%s(SELECT %s,cursor_item.id FROM space_library_items cursor_item JOIN library_files cursor_file ON cursor_file.id=cursor_item.file_id JOIN library_blobs cursor_blob ON cursor_blob.id=cursor_file.blob_id WHERE cursor_item.id=%s AND cursor_item.space_id=$1)", sortExpression, operator, subquerySortExpression, placeholder))
		}
		args = append(args, query.Limit)
		statement := `SELECT i.id,i.space_id,i.file_id,i.contributing_user_id,i.display_name,i.caption,i.tags,i.favorite,i.hidden,i.date_override,COALESCE(i.location_override,'null'::jsonb),i.contributor_information,COALESCE(i.current_edit_version_id,''),i.added_by_user_id,i.lifecycle_state,i.added_at,i.trashed_at,i.recover_until,i.version,i.updated_at,
			f.id,f.blob_id,f.security_domain_id,f.uploader_user_id,f.original_filename,f.intrinsic_metadata,f.lifecycle_state,f.original_uploaded_at,f.version
			FROM space_library_items i JOIN library_files f ON f.id=i.file_id JOIN library_blobs b ON b.id=f.blob_id
			WHERE ` + strings.Join(conditions, " AND ") + ` ORDER BY ` + sortExpression + ` ` + strings.ToUpper(query.Direction) + `,i.id ` + strings.ToUpper(query.Direction) + ` LIMIT $` + strconv.Itoa(len(args))
		rows, err := tx.QueryContext(ctx, statement, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item SpaceLibraryItem
			if err := scanSpaceLibraryItem(rows, &item); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}

func libraryMediaSubtypeCondition(kind string) string {
	metadata := `lower(f.original_filename||' '||f.intrinsic_metadata::text||' '||COALESCE((SELECT string_agg(d.metadata::text,' ') FROM library_derivatives d WHERE d.space_library_item_id=i.id AND d.lifecycle_state='ready' AND d.kind='ai_metadata'),''))`
	switch kind {
	case "selfies":
		return "b.server_detected_mime_type LIKE 'image/%' AND " + metadata + " ~ '(selfie|front.camera)'"
	case "live-photos":
		return "(EXISTS(SELECT 1 FROM space_library_asset_stacks asset_stack WHERE asset_stack.space_id=i.space_id AND asset_stack.cover_item_id=i.id AND asset_stack.kind='live_photo' AND asset_stack.lifecycle_state='ready') OR b.server_detected_mime_type LIKE 'image/%' AND " + metadata + " ~ '(live.photo|motion.photo)')"
	case "portraits":
		return "b.server_detected_mime_type LIKE 'image/%' AND " + metadata + " ~ '(portrait|depth.effect)'"
	case "panoramas":
		return "b.server_detected_mime_type LIKE 'image/%' AND " + metadata + " ~ '(panorama|pano)'"
	case "slo-mo":
		return "b.server_detected_mime_type LIKE 'video/%' AND " + metadata + " ~ '(slo.mo|slow.motion|high.frame.rate)'"
	case "cinematic":
		return "b.server_detected_mime_type LIKE 'video/%' AND " + metadata + " ~ '(cinematic|depth.video)'"
	case "bursts":
		return "(EXISTS(SELECT 1 FROM space_library_asset_stacks asset_stack WHERE asset_stack.space_id=i.space_id AND asset_stack.cover_item_id=i.id AND asset_stack.kind='burst' AND asset_stack.lifecycle_state='ready') OR b.server_detected_mime_type LIKE 'image/%' AND " + metadata + " ~ '(burst|burst.identifier)')"
	case "raw":
		return "(EXISTS(SELECT 1 FROM space_library_asset_stacks asset_stack WHERE asset_stack.space_id=i.space_id AND asset_stack.cover_item_id=i.id AND asset_stack.kind='raw_pair' AND asset_stack.lifecycle_state='ready') OR lower(f.original_filename) ~ '\\.(dng|cr2|cr3|nef|nrw|arw|srf|sr2|raf|rw2|orf|pef|x3f)$')"
	case "screenshots":
		return "b.server_detected_mime_type LIKE 'image/%' AND " + metadata + " ~ '(screenshot|screen.shot)'"
	case "screen-recordings":
		return "b.server_detected_mime_type LIKE 'video/%' AND " + metadata + " ~ '(screen.recording|screen.capture)'"
	case "spatial":
		return "(b.server_detected_mime_type LIKE 'image/%' OR b.server_detected_mime_type LIKE 'video/%') AND " + metadata + " ~ '(spatial|stereo.scopic|vision.pro)'"
	default:
		return "FALSE"
	}
}
