package db

import (
	"fmt"
	"strconv"
	"strings"
)

// libraryItemFilter accumulates the WHERE conditions and positional arguments of a library item listing.
type libraryItemFilter struct {
	args       []any
	conditions []string
}

// newLibraryItemFilter starts a listing scoped to the space and state and visible to the viewer.
func newLibraryItemFilter(spaceID, state, userID string) *libraryItemFilter {
	filter := &libraryItemFilter{
		args:       []any{spaceID, state},
		conditions: []string{"i.space_id=$1", "i.lifecycle_state=$2"},
	}
	if state == "ready" {
		filter.where("NOT EXISTS(SELECT 1 FROM space_library_asset_stack_members stack_member JOIN space_library_asset_stacks asset_stack ON asset_stack.id=stack_member.stack_id WHERE stack_member.space_library_item_id=i.id AND asset_stack.lifecycle_state='ready' AND asset_stack.cover_item_id<>i.id)")
	}
	viewerPlaceholder := filter.add(userID)
	filter.where("(i.audience_kind='space' OR EXISTS(SELECT 1 FROM space_conversation_members audience_member WHERE audience_member.conversation_id=i.audience_conversation_id AND audience_member.actor_kind='person' AND audience_member.user_id=" + viewerPlaceholder + "))")
	return filter
}

func (f *libraryItemFilter) add(value any) string {
	f.args = append(f.args, value)
	return fmt.Sprintf("$%d", len(f.args))
}

func (f *libraryItemFilter) where(condition string) {
	f.conditions = append(f.conditions, condition)
}

// applyAttributeFilters adds visibility, favorite, tag and free-text search conditions.
func (f *libraryItemFilter) applyAttributeFilters(query LibraryItemQuery, structuredSearch parsedLibrarySearch) {
	switch query.Visibility {
	case "visible":
		f.where("i.hidden=FALSE")
	case "hidden":
		f.where("i.hidden=TRUE")
	}
	if query.Favorite {
		f.where("i.favorite=TRUE")
	}
	if structuredSearch.Favorite != nil {
		f.where("i.favorite=" + f.add(*structuredSearch.Favorite))
	}
	for _, tag := range structuredSearch.Tags {
		f.where("EXISTS(SELECT 1 FROM jsonb_array_elements_text(i.tags) search_tag WHERE lower(search_tag)=lower(" + f.add(tag) + "))")
	}
	if query.Search != "" {
		placeholder := f.add(query.Search)
		f.where("(to_tsvector('simple',i.display_name||' '||i.caption||' '||i.tags::text) @@ plainto_tsquery('simple'," + placeholder + ") OR to_tsvector('simple',f.original_filename||' '||f.intrinsic_metadata::text) @@ plainto_tsquery('simple'," + placeholder + "))")
	}
}

// applyMediaFilter adds the media type or media subtype condition and rejects unknown types.
func (f *libraryItemFilter) applyMediaFilter(mediaType string) error {
	switch mediaType {
	case "image", "video", "audio":
		f.where("b.server_detected_mime_type LIKE " + f.add(mediaType+"/%"))
	case "document":
		f.where("b.server_detected_mime_type NOT LIKE 'image/%' AND b.server_detected_mime_type NOT LIKE 'video/%' AND b.server_detected_mime_type NOT LIKE 'audio/%'")
	case "selfies", "live-photos", "portraits", "panoramas", "slo-mo", "cinematic", "bursts", "raw", "screenshots", "screen-recordings", "spatial":
		f.where(libraryMediaSubtypeCondition(mediaType))
	case "":
	default:
		return ErrLibraryInvalid
	}
	return nil
}

// applyAlbumAndUtilityFilters adds the album, utility and structured album conditions. The
// utility filters may replace the sort expressions, so the updated pair is returned.
func (f *libraryItemFilter) applyAlbumAndUtilityFilters(query LibraryItemQuery, searchAlbum, userID, sortExpression, subquerySortExpression string) (string, string, error) {
	if query.AlbumID != "" {
		f.where("EXISTS(SELECT 1 FROM space_album_items album_item JOIN space_albums album ON album.id=album_item.album_id WHERE album_item.space_library_item_id=i.id AND album.id=" + f.add(query.AlbumID) + " AND album.space_id=i.space_id)")
	}
	sortExpression, subquerySortExpression, err := f.applyUtilityFilter(query.Utility, userID, sortExpression, subquerySortExpression)
	if err != nil {
		return "", "", err
	}
	if searchAlbum != "" {
		placeholder := f.add(searchAlbum)
		f.where("EXISTS(SELECT 1 FROM space_album_items search_album_item JOIN space_albums search_album ON search_album.id=search_album_item.album_id WHERE search_album_item.space_library_item_id=i.id AND search_album.space_id=i.space_id AND (search_album.id=" + placeholder + " OR lower(search_album.name)=lower(" + placeholder + ")))")
	}
	return sortExpression, subquerySortExpression, nil
}

// applyUtilityFilter adds the condition for a utility collection and returns the sort expressions it uses.
func (f *libraryItemFilter) applyUtilityFilter(utility, userID, sortExpression, subquerySortExpression string) (string, string, error) {
	switch utility {
	case "":
	case "recently-viewed":
		recentViewerPlaceholder := f.add(userID)
		f.where("EXISTS(SELECT 1 FROM space_library_item_views item_view WHERE item_view.space_id=i.space_id AND item_view.space_library_item_id=i.id AND item_view.user_id=" + recentViewerPlaceholder + ")")
		sortExpression = "(SELECT item_view.last_viewed_at FROM space_library_item_views item_view WHERE item_view.space_id=i.space_id AND item_view.space_library_item_id=i.id AND item_view.user_id=" + recentViewerPlaceholder + ")"
		subquerySortExpression = "(SELECT item_view.last_viewed_at FROM space_library_item_views item_view WHERE item_view.space_id=cursor_item.space_id AND item_view.space_library_item_id=cursor_item.id AND item_view.user_id=" + recentViewerPlaceholder + ")"
	case "recently-edited":
		f.where("i.current_edit_version_id IS NOT NULL")
		sortExpression, subquerySortExpression = "i.updated_at", "cursor_item.updated_at"
	case "recently-shared":
		f.where("(EXISTS(SELECT 1 FROM space_library_grants grant_record WHERE grant_record.source_space_id=i.space_id AND grant_record.source_item_id=i.id AND grant_record.state='active') OR EXISTS(SELECT 1 FROM space_message_library_references message_reference WHERE message_reference.space_id=i.space_id AND message_reference.space_library_item_id=i.id))")
	case "recently-saved":
		f.where("EXISTS(SELECT 1 FROM space_message_attachments saved_attachment WHERE saved_attachment.space_id=i.space_id AND saved_attachment.promoted_item_id=i.id)")
		sortExpression, subquerySortExpression = "i.added_at", "cursor_item.added_at"
	case "recovered":
		f.where("EXISTS(SELECT 1 FROM space_library_audit_events recovery_event WHERE recovery_event.space_id=i.space_id AND recovery_event.target_kind='library_item' AND recovery_event.target_id=i.id AND recovery_event.action='library.item.restored' AND recovery_event.outcome='success')")
		sortExpression, subquerySortExpression = "i.updated_at", "cursor_item.updated_at"
	case "imports":
		f.where("EXISTS(SELECT 1 FROM space_library_imports import_record WHERE import_record.destination_space_id=i.space_id AND import_record.destination_item_id=i.id AND import_record.state='ready')")
	case "featured":
		f.where("b.server_detected_mime_type LIKE 'image/%' AND (i.favorite OR EXISTS(SELECT 1 FROM library_derivatives featured_derivative WHERE featured_derivative.space_library_item_id=i.id AND featured_derivative.lifecycle_state='ready' AND featured_derivative.kind='ai_metadata' AND lower(featured_derivative.metadata::text) ~ '(featured|aesthetic|best shot|high quality)'))")
	case "screenshots":
		f.where("(lower(f.original_filename) LIKE '%screenshot%' OR lower(f.intrinsic_metadata::text) LIKE '%screenshot%')")
	case "documents":
		f.where("b.server_detected_mime_type NOT LIKE 'image/%' AND b.server_detected_mime_type NOT LIKE 'video/%' AND b.server_detected_mime_type NOT LIKE 'audio/%'")
	case "receipts", "handwriting", "illustrations", "qr-codes":
		keyword := map[string]string{"receipts": "receipt", "handwriting": "handwrit", "illustrations": "illustration", "qr-codes": "qr"}[utility]
		f.where("EXISTS(SELECT 1 FROM library_derivatives intelligence WHERE intelligence.space_library_item_id=i.id AND intelligence.lifecycle_state='ready' AND intelligence.kind='ai_metadata' AND lower(intelligence.metadata::text) LIKE " + f.add("%"+keyword+"%") + ")")
	default:
		return "", "", ErrLibraryInvalid
	}
	return sortExpression, subquerySortExpression, nil
}

// applyDateAndCursorFilters adds the capture date range and the keyset pagination cursor.
func (f *libraryItemFilter) applyDateAndCursorFilters(query LibraryItemQuery, sortExpression, subquerySortExpression, sortDirection string) {
	if query.DateFrom != nil {
		f.where("COALESCE(i.date_override,f.intrinsic_capture_at,f.original_uploaded_at)>=" + f.add(*query.DateFrom))
	}
	if query.DateTo != nil {
		f.where("COALESCE(i.date_override,f.intrinsic_capture_at,f.original_uploaded_at)<" + f.add(*query.DateTo))
	}
	if query.After != "" {
		operator := "<"
		if sortDirection == "ASC" {
			operator = ">"
		}
		placeholder := f.add(query.After)
		f.where(fmt.Sprintf("(%s,i.id)%s(SELECT %s,cursor_item.id FROM space_library_items cursor_item JOIN library_files cursor_file ON cursor_file.id=cursor_item.file_id JOIN library_blobs cursor_blob ON cursor_blob.id=cursor_file.blob_id WHERE cursor_item.id=%s AND cursor_item.space_id=$1)", sortExpression, operator, subquerySortExpression, placeholder))
	}
}

// buildLibraryItemsStatement appends the page limit to the arguments and returns the listing query.
func buildLibraryItemsStatement(filter *libraryItemFilter, limit int, sortExpression, sortDirection string) string {
	filter.args = append(filter.args, limit)
	return `SELECT i.id,i.space_id,i.file_id,i.contributing_user_id,i.display_name,i.caption,i.tags,i.favorite,i.hidden,i.date_override,COALESCE(i.location_override,'null'::jsonb),i.contributor_information,COALESCE(i.current_edit_version_id,''),i.added_by_user_id,i.lifecycle_state,i.added_at,i.trashed_at,i.recover_until,i.version,i.updated_at,
			f.id,f.blob_id,f.security_domain_id,f.uploader_user_id,f.original_filename,f.intrinsic_metadata,f.lifecycle_state,f.original_uploaded_at,f.version
			FROM space_library_items i JOIN library_files f ON f.id=i.file_id JOIN library_blobs b ON b.id=f.blob_id
			WHERE ` + strings.Join(filter.conditions, " AND ") + ` ORDER BY ` + sortExpression + ` ` + sortDirection + `,i.id ` + sortDirection + ` LIMIT $` + strconv.Itoa(len(filter.args))
}
