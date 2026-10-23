package db

import (
	"context"
	"database/sql"
	"strings"
)

func (db *Database) LibraryItems(ctx context.Context, userID, spaceID string, query LibraryItemQuery) ([]SpaceLibraryItem, error) {
	query, err := normalizeLibraryItemQuery(query)
	if err != nil {
		return nil, err
	}
	structuredSearch, err := ParseLibrarySearch(query.Search)
	if err != nil {
		return nil, err
	}
	query = applyStructuredLibrarySearch(query, structuredSearch)
	sortDirection := librarySortDirection(query.Direction)
	query.Visibility = normalizeLibraryVisibility(query.Visibility)
	state := libraryItemState(query.Collection)
	var sortExpression, subquerySortExpression string
	query.Sort, sortExpression, subquerySortExpression = librarySortExpressions(query.Sort)
	items := []SpaceLibraryItem{}
	err = db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionLibraryView); err != nil {
			return err
		}
		filter := newLibraryItemFilter(spaceID, state, userID, itemViewer(ctx, userID))
		filter.applyAttributeFilters(query, structuredSearch)
		if err := filter.applyMediaFilter(query.MediaType); err != nil {
			return err
		}
		sortExpression, subquerySortExpression, err = filter.applyUtilityFilter(query.Utility, userID, sortExpression, subquerySortExpression)
		if err != nil {
			return err
		}
		filter.applyDateAndCursorFilters(query, sortExpression, subquerySortExpression, sortDirection)
		statement := buildLibraryItemsStatement(filter, query.Limit, sortExpression, sortDirection)
		rows, err := tx.QueryContext(ctx, statement, filter.args...)
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

// normalizeLibraryItemQuery clamps the page size and trims the search text, rejecting overlong searches.
func normalizeLibraryItemQuery(query LibraryItemQuery) (LibraryItemQuery, error) {
	if query.Limit < 1 || query.Limit > 200 {
		query.Limit = 100
	}
	query.Search = strings.TrimSpace(query.Search)
	if len([]rune(query.Search)) > 240 {
		return query, ErrLibraryInvalid
	}
	return query, nil
}

// applyStructuredLibrarySearch lets operators parsed from the search text override the query fields.
func applyStructuredLibrarySearch(query LibraryItemQuery, structuredSearch parsedLibrarySearch) LibraryItemQuery {
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
	return query
}

func librarySortDirection(direction string) string {
	if direction == "asc" {
		return "ASC"
	}
	return "DESC"
}

func normalizeLibraryVisibility(visibility string) string {
	if visibility != "all" && visibility != "hidden" {
		return "visible"
	}
	return visibility
}

func libraryItemState(collection string) string {
	if collection == "recently-deleted" {
		return "trash"
	}
	return "ready"
}

// librarySortExpressions returns the normalized sort key and the matching outer and cursor-subquery expressions.
func librarySortExpressions(sort string) (string, string, string) {
	switch sort {
	case "date-captured":
		return sort, "COALESCE(i.date_override,f.intrinsic_capture_at,f.original_uploaded_at)", "COALESCE(cursor_item.date_override,cursor_file.intrinsic_capture_at,cursor_file.original_uploaded_at)"
	case "name":
		return sort, "lower(i.display_name)", "lower(cursor_item.display_name)"
	case "size":
		return sort, "b.byte_size", "cursor_blob.byte_size"
	default:
		return "recently-added", "i.added_at", "cursor_item.added_at"
	}
}

func libraryMediaSubtypeCondition(kind string) string {
	metadata := `lower(f.original_filename||' '||f.intrinsic_metadata::text||' '||COALESCE((SELECT string_agg(d.metadata::text,' ') FROM library_derivatives d WHERE d.space_library_item_id=i.id AND d.lifecycle_state='ready' AND d.kind='ai_metadata'),''))`
	switch kind {
	case "selfies":
		return "b.server_detected_mime_type LIKE 'image/%' AND " + metadata + " ~ '(selfie|front.camera)'"
	case "live-photos":
		return "b.server_detected_mime_type LIKE 'image/%' AND " + metadata + " ~ '(live.photo|motion.photo)'"
	case "portraits":
		return "b.server_detected_mime_type LIKE 'image/%' AND " + metadata + " ~ '(portrait|depth.effect)'"
	case "panoramas":
		return "b.server_detected_mime_type LIKE 'image/%' AND " + metadata + " ~ '(panorama|pano)'"
	case "slo-mo":
		return "b.server_detected_mime_type LIKE 'video/%' AND " + metadata + " ~ '(slo.mo|slow.motion|high.frame.rate)'"
	case "cinematic":
		return "b.server_detected_mime_type LIKE 'video/%' AND " + metadata + " ~ '(cinematic|depth.video)'"
	case "bursts":
		return "b.server_detected_mime_type LIKE 'image/%' AND " + metadata + " ~ '(burst|burst.identifier)'"
	case "raw":
		return "lower(f.original_filename) ~ '\\.(dng|cr2|cr3|nef|nrw|arw|srf|sr2|raf|rw2|orf|pef|x3f)$'"
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
