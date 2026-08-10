package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

type LibrarySearchFacet struct {
	Value string `json:"value"`
	Label string `json:"label"`
	Count int    `json:"count"`
}

type LibrarySearchFacets struct {
	Total           int                  `json:"total"`
	Favorites       int                  `json:"favorites"`
	Hidden          int                  `json:"hidden"`
	RecentlyDeleted int                  `json:"recently_deleted"`
	Tags            []LibrarySearchFacet `json:"tags"`
	MediaTypes      []LibrarySearchFacet `json:"media_types"`
	Years           []LibrarySearchFacet `json:"years"`
	Albums          []LibrarySearchFacet `json:"albums"`
	Utilities       []LibrarySearchFacet `json:"utilities"`
}

func (db *Database) LibraryFacets(ctx context.Context, userID, spaceID, prefix string) (*LibrarySearchFacets, error) {
	prefix = strings.TrimSpace(prefix)
	if len([]rune(prefix)) > 120 {
		return nil, ErrLibraryInvalid
	}
	out := &LibrarySearchFacets{Tags: []LibrarySearchFacet{}, MediaTypes: []LibrarySearchFacet{}, Years: []LibrarySearchFacet{}, Albums: []LibrarySearchFacet{}, Utilities: []LibrarySearchFacet{}}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionLibraryView); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FILTER(WHERE i.lifecycle_state='ready' AND i.hidden=FALSE AND NOT EXISTS(SELECT 1 FROM space_library_asset_stack_members sm JOIN space_library_asset_stacks s ON s.id=sm.stack_id WHERE sm.space_library_item_id=i.id AND s.lifecycle_state='ready' AND s.cover_item_id<>i.id)),count(*) FILTER(WHERE i.lifecycle_state='ready' AND i.hidden=FALSE AND i.favorite=TRUE AND NOT EXISTS(SELECT 1 FROM space_library_asset_stack_members sm JOIN space_library_asset_stacks s ON s.id=sm.stack_id WHERE sm.space_library_item_id=i.id AND s.lifecycle_state='ready' AND s.cover_item_id<>i.id)),count(*) FILTER(WHERE i.lifecycle_state='ready' AND i.hidden=TRUE AND NOT EXISTS(SELECT 1 FROM space_library_asset_stack_members sm JOIN space_library_asset_stacks s ON s.id=sm.stack_id WHERE sm.space_library_item_id=i.id AND s.lifecycle_state='ready' AND s.cover_item_id<>i.id)),count(*) FILTER(WHERE i.lifecycle_state='trash') FROM space_library_items i WHERE i.space_id=$1`, spaceID).Scan(&out.Total, &out.Favorites, &out.Hidden, &out.RecentlyDeleted); err != nil {
			return err
		}
		like := "%" + prefix + "%"
		queries := []struct {
			destination *[]LibrarySearchFacet
			statement   string
			args        []any
		}{
			{&out.Tags, `SELECT tag,tag,count(*) FROM space_library_items i CROSS JOIN LATERAL jsonb_array_elements_text(i.tags) tag WHERE i.space_id=$1 AND i.lifecycle_state='ready' AND i.hidden=FALSE AND NOT EXISTS(SELECT 1 FROM space_library_asset_stack_members sm JOIN space_library_asset_stacks s ON s.id=sm.stack_id WHERE sm.space_library_item_id=i.id AND s.lifecycle_state='ready' AND s.cover_item_id<>i.id) AND ($2='' OR tag ILIKE $3) GROUP BY tag ORDER BY count(*) DESC,lower(tag) LIMIT 12`, []any{spaceID, prefix, like}},
			{&out.MediaTypes, `SELECT kind,initcap(kind),count(*) FROM (SELECT CASE WHEN b.server_detected_mime_type LIKE 'image/%' THEN 'image' WHEN b.server_detected_mime_type LIKE 'video/%' THEN 'video' WHEN b.server_detected_mime_type LIKE 'audio/%' THEN 'audio' ELSE 'document' END kind FROM space_library_items i JOIN library_files f ON f.id=i.file_id JOIN library_blobs b ON b.id=f.blob_id WHERE i.space_id=$1 AND i.lifecycle_state='ready' AND i.hidden=FALSE AND NOT EXISTS(SELECT 1 FROM space_library_asset_stack_members sm JOIN space_library_asset_stacks s ON s.id=sm.stack_id WHERE sm.space_library_item_id=i.id AND s.lifecycle_state='ready' AND s.cover_item_id<>i.id)) media WHERE ($2='' OR kind ILIKE $3) GROUP BY kind ORDER BY count(*) DESC,kind`, []any{spaceID, prefix, like}},
			{&out.Years, `SELECT captured_year,captured_year,count(*) FROM (SELECT extract(year FROM COALESCE(i.date_override,f.intrinsic_capture_at,f.original_uploaded_at))::int::text captured_year FROM space_library_items i JOIN library_files f ON f.id=i.file_id WHERE i.space_id=$1 AND i.lifecycle_state='ready' AND i.hidden=FALSE AND NOT EXISTS(SELECT 1 FROM space_library_asset_stack_members sm JOIN space_library_asset_stacks s ON s.id=sm.stack_id WHERE sm.space_library_item_id=i.id AND s.lifecycle_state='ready' AND s.cover_item_id<>i.id)) dates WHERE ($2='' OR captured_year ILIKE $3) GROUP BY captured_year ORDER BY captured_year DESC LIMIT 12`, []any{spaceID, prefix, like}},
			{&out.Albums, `SELECT a.id,a.name,count(ai.space_library_item_id) FROM space_albums a LEFT JOIN space_album_items ai ON ai.album_id=a.id LEFT JOIN space_library_items i ON i.id=ai.space_library_item_id AND i.lifecycle_state='ready' AND i.hidden=FALSE WHERE a.space_id=$1 AND ($2='' OR a.name ILIKE $3) GROUP BY a.id,a.name ORDER BY count(ai.space_library_item_id) DESC,lower(a.name) LIMIT 12`, []any{spaceID, prefix, like}},
			{&out.Utilities, `SELECT utility,label,item_count FROM (
				SELECT 'recently-viewed' utility,'Recently Viewed' label,count(*) item_count,1 sort_order FROM space_library_items i WHERE i.space_id=$1 AND i.lifecycle_state='ready' AND i.hidden=FALSE AND EXISTS(SELECT 1 FROM space_library_item_views v WHERE v.space_id=i.space_id AND v.space_library_item_id=i.id AND v.user_id=$2)
				UNION ALL SELECT 'recently-edited','Recently Edited',count(*),2 FROM space_library_items i WHERE i.space_id=$1 AND i.lifecycle_state='ready' AND i.hidden=FALSE AND i.current_edit_version_id IS NOT NULL
				UNION ALL SELECT 'recently-shared','Recently Shared',count(*),3 FROM space_library_items i WHERE i.space_id=$1 AND i.lifecycle_state='ready' AND i.hidden=FALSE AND (EXISTS(SELECT 1 FROM space_library_grants g WHERE g.source_space_id=i.space_id AND g.source_item_id=i.id AND g.state='active') OR EXISTS(SELECT 1 FROM space_message_library_references r WHERE r.space_id=i.space_id AND r.space_library_item_id=i.id))
				UNION ALL SELECT 'recently-saved','Recently Saved',count(*),4 FROM space_library_items i WHERE i.space_id=$1 AND i.lifecycle_state='ready' AND i.hidden=FALSE AND EXISTS(SELECT 1 FROM space_message_attachments a WHERE a.space_id=i.space_id AND a.promoted_item_id=i.id)
				UNION ALL SELECT 'recovered','Recovered',count(*),5 FROM space_library_items i WHERE i.space_id=$1 AND i.lifecycle_state='ready' AND i.hidden=FALSE AND EXISTS(SELECT 1 FROM space_library_audit_events e WHERE e.space_id=i.space_id AND e.target_kind='library_item' AND e.target_id=i.id AND e.action='library.item.restored' AND e.outcome='success')
				UNION ALL SELECT 'imports','Imports',count(*),6 FROM space_library_items i WHERE i.space_id=$1 AND i.lifecycle_state='ready' AND i.hidden=FALSE AND EXISTS(SELECT 1 FROM space_library_imports h WHERE h.destination_space_id=i.space_id AND h.destination_item_id=i.id AND h.state='ready')
				UNION ALL SELECT 'featured','Featured Photos',count(*),7 FROM space_library_items i JOIN library_files f ON f.id=i.file_id JOIN library_blobs b ON b.id=f.blob_id WHERE i.space_id=$1 AND i.lifecycle_state='ready' AND i.hidden=FALSE AND NOT EXISTS(SELECT 1 FROM space_library_asset_stack_members sm JOIN space_library_asset_stacks s ON s.id=sm.stack_id WHERE sm.space_library_item_id=i.id AND s.lifecycle_state='ready' AND s.cover_item_id<>i.id) AND b.server_detected_mime_type LIKE 'image/%' AND (i.favorite OR EXISTS(SELECT 1 FROM library_derivatives d WHERE d.space_library_item_id=i.id AND d.lifecycle_state='ready' AND d.kind='ai_metadata' AND lower(d.metadata::text) ~ '(featured|aesthetic|best shot|high quality)'))
				UNION ALL SELECT 'screenshots','Screenshots',count(*),8 FROM space_library_items i JOIN library_files f ON f.id=i.file_id WHERE i.space_id=$1 AND i.lifecycle_state='ready' AND i.hidden=FALSE AND (lower(f.original_filename) LIKE '%screenshot%' OR lower(f.intrinsic_metadata::text) LIKE '%screenshot%')
				UNION ALL SELECT 'documents','Documents',count(*),6 FROM space_library_items i JOIN library_files f ON f.id=i.file_id JOIN library_blobs b ON b.id=f.blob_id WHERE i.space_id=$1 AND i.lifecycle_state='ready' AND i.hidden=FALSE AND b.server_detected_mime_type NOT LIKE 'image/%' AND b.server_detected_mime_type NOT LIKE 'video/%' AND b.server_detected_mime_type NOT LIKE 'audio/%'
				UNION ALL SELECT 'receipts','Receipts',count(*),7 FROM space_library_items i WHERE i.space_id=$1 AND i.lifecycle_state='ready' AND i.hidden=FALSE AND EXISTS(SELECT 1 FROM library_derivatives d WHERE d.space_library_item_id=i.id AND d.lifecycle_state='ready' AND d.kind='ai_metadata' AND lower(d.metadata::text) LIKE '%receipt%')
				UNION ALL SELECT 'handwriting','Handwriting',count(*),8 FROM space_library_items i WHERE i.space_id=$1 AND i.lifecycle_state='ready' AND i.hidden=FALSE AND EXISTS(SELECT 1 FROM library_derivatives d WHERE d.space_library_item_id=i.id AND d.lifecycle_state='ready' AND d.kind='ai_metadata' AND lower(d.metadata::text) LIKE '%handwrit%')
				UNION ALL SELECT 'illustrations','Illustrations',count(*),9 FROM space_library_items i WHERE i.space_id=$1 AND i.lifecycle_state='ready' AND i.hidden=FALSE AND EXISTS(SELECT 1 FROM library_derivatives d WHERE d.space_library_item_id=i.id AND d.lifecycle_state='ready' AND d.kind='ai_metadata' AND lower(d.metadata::text) LIKE '%illustration%')
				UNION ALL SELECT 'qr-codes','QR Codes',count(*),10 FROM space_library_items i WHERE i.space_id=$1 AND i.lifecycle_state='ready' AND i.hidden=FALSE AND EXISTS(SELECT 1 FROM library_derivatives d WHERE d.space_library_item_id=i.id AND d.lifecycle_state='ready' AND d.kind='ai_metadata' AND lower(d.metadata::text) LIKE '%qr%')
			) utility_rows WHERE item_count>0 AND ($3='' OR label ILIKE $4) ORDER BY sort_order`, []any{spaceID, userID, prefix, like}},
		}
		for _, query := range queries {
			rows, err := tx.QueryContext(ctx, query.statement, query.args...)
			if err != nil {
				return fmt.Errorf("library facets: %w", err)
			}
			for rows.Next() {
				var facet LibrarySearchFacet
				if err := rows.Scan(&facet.Value, &facet.Label, &facet.Count); err != nil {
					_ = rows.Close()
					return err
				}
				*query.destination = append(*query.destination, facet)
			}
			if err := rows.Close(); err != nil {
				return err
			}
		}
		mediaSubtypes := []struct{ value, label string }{
			{"selfies", "Selfies"}, {"live-photos", "Live Photos"}, {"portraits", "Portraits"}, {"panoramas", "Panoramas"}, {"slo-mo", "Slo-mo"}, {"cinematic", "Cinematic"}, {"bursts", "Bursts"}, {"raw", "RAW"}, {"screenshots", "Screenshots"}, {"screen-recordings", "Screen Recordings"}, {"spatial", "Spatial"},
		}
		for _, subtype := range mediaSubtypes {
			var count int
			statement := `SELECT count(*) FROM space_library_items i JOIN library_files f ON f.id=i.file_id JOIN library_blobs b ON b.id=f.blob_id WHERE i.space_id=$1 AND i.lifecycle_state='ready' AND i.hidden=FALSE AND NOT EXISTS(SELECT 1 FROM space_library_asset_stack_members sm JOIN space_library_asset_stacks s ON s.id=sm.stack_id WHERE sm.space_library_item_id=i.id AND s.lifecycle_state='ready' AND s.cover_item_id<>i.id) AND ` + libraryMediaSubtypeCondition(subtype.value)
			if err := tx.QueryRowContext(ctx, statement, spaceID).Scan(&count); err != nil {
				return err
			}
			if count > 0 {
				out.MediaTypes = append(out.MediaTypes, LibrarySearchFacet{Value: subtype.value, Label: subtype.label, Count: count})
			}
		}
		return nil
	})
	return out, err
}
