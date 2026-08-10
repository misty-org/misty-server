package db

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"time"
)

var libraryMemoryIDPattern = regexp.MustCompile(`^\d{4}-\d{2}$`)

var libraryDayIDPattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

var libraryYearIDPattern = regexp.MustCompile(`^\d{4}$`)

var libraryMapIDPattern = regexp.MustCompile(`^-?\d{1,3}(?:\.\d{1,6})?,-?\d{1,3}(?:\.\d{1,6})?$`)

type LibraryDiscoveryGroup struct {
	ID                string     `json:"id"`
	Kind              string     `json:"kind"`
	Title             string     `json:"title"`
	Subtitle          string     `json:"subtitle"`
	CoverItemID       string     `json:"cover_item_id,omitempty"`
	ItemCount         int        `json:"item_count"`
	StartAt           *time.Time `json:"start_at,omitempty"`
	EndAt             *time.Time `json:"end_at,omitempty"`
	MusicItemID       string     `json:"music_item_id,omitempty"`
	PlaybackSeconds   float64    `json:"playback_seconds,omitempty"`
	PreferenceVersion int64      `json:"preference_version,omitempty"`
}

type LibraryDiscovery struct {
	RecentDays []LibraryDiscoveryGroup `json:"recent_days"`
	Months     []LibraryDiscoveryGroup `json:"months"`
	Years      []LibraryDiscoveryGroup `json:"years"`
	Memories   []LibraryDiscoveryGroup `json:"memories"`
	Trips      []LibraryDiscoveryGroup `json:"trips"`
	Duplicates []LibraryDiscoveryGroup `json:"duplicates"`
}

func (db *Database) LibraryDiscovery(ctx context.Context, userID, spaceID string) (*LibraryDiscovery, error) {
	out := &LibraryDiscovery{RecentDays: []LibraryDiscoveryGroup{}, Months: []LibraryDiscoveryGroup{}, Years: []LibraryDiscoveryGroup{}, Memories: []LibraryDiscoveryGroup{}, Trips: []LibraryDiscoveryGroup{}, Duplicates: []LibraryDiscoveryGroup{}}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionLibraryView); err != nil {
			return err
		}
		dayRows, err := tx.QueryContext(ctx, `SELECT to_char(bucket,'YYYY-MM-DD'),bucket,max(captured_at),(array_agg(id ORDER BY captured_at DESC,id))[1],count(*)
			FROM (SELECT i.id,date_trunc('day',COALESCE(i.date_override,f.intrinsic_capture_at,f.original_uploaded_at)) bucket,COALESCE(i.date_override,f.intrinsic_capture_at,f.original_uploaded_at) captured_at
			FROM space_library_items i JOIN library_files f ON f.id=i.file_id WHERE i.space_id=$1 AND i.lifecycle_state='ready' AND i.hidden=FALSE) visible
			GROUP BY bucket ORDER BY bucket DESC LIMIT 30`, spaceID)
		if err != nil {
			return err
		}
		for dayRows.Next() {
			var group LibraryDiscoveryGroup
			var start, end time.Time
			if err := dayRows.Scan(&group.ID, &start, &end, &group.CoverItemID, &group.ItemCount); err != nil {
				_ = dayRows.Close()
				return err
			}
			group.Kind, group.Title, group.Subtitle, group.StartAt, group.EndAt = "day", start.Format("Monday, January 2"), fmt.Sprintf("%d items", group.ItemCount), &start, &end
			out.RecentDays = append(out.RecentDays, group)
		}
		if err := dayRows.Close(); err != nil {
			return err
		}

		monthRows, err := tx.QueryContext(ctx, `SELECT to_char(bucket,'YYYY-MM'),bucket,max(captured_at),(array_agg(id ORDER BY captured_at DESC,id))[1],count(*)
			FROM (SELECT i.id,date_trunc('month',COALESCE(i.date_override,f.intrinsic_capture_at,f.original_uploaded_at)) bucket,COALESCE(i.date_override,f.intrinsic_capture_at,f.original_uploaded_at) captured_at
			FROM space_library_items i JOIN library_files f ON f.id=i.file_id WHERE i.space_id=$1 AND i.lifecycle_state='ready' AND i.hidden=FALSE) visible
			GROUP BY bucket ORDER BY bucket DESC LIMIT 120`, spaceID)
		if err != nil {
			return err
		}
		for monthRows.Next() {
			var group LibraryDiscoveryGroup
			var start, end time.Time
			if err := monthRows.Scan(&group.ID, &start, &end, &group.CoverItemID, &group.ItemCount); err != nil {
				_ = monthRows.Close()
				return err
			}
			group.Kind, group.Title, group.Subtitle, group.StartAt, group.EndAt = "month", start.Format("January 2006"), fmt.Sprintf("%d items", group.ItemCount), &start, &end
			out.Months = append(out.Months, group)
		}
		if err := monthRows.Close(); err != nil {
			return err
		}

		yearRows, err := tx.QueryContext(ctx, `SELECT to_char(bucket,'YYYY'),bucket,max(captured_at),(array_agg(id ORDER BY captured_at DESC,id))[1],count(*)
			FROM (SELECT i.id,date_trunc('year',COALESCE(i.date_override,f.intrinsic_capture_at,f.original_uploaded_at)) bucket,COALESCE(i.date_override,f.intrinsic_capture_at,f.original_uploaded_at) captured_at
			FROM space_library_items i JOIN library_files f ON f.id=i.file_id WHERE i.space_id=$1 AND i.lifecycle_state='ready' AND i.hidden=FALSE) visible
			GROUP BY bucket ORDER BY bucket DESC LIMIT 100`, spaceID)
		if err != nil {
			return err
		}
		for yearRows.Next() {
			var group LibraryDiscoveryGroup
			var start, end time.Time
			if err := yearRows.Scan(&group.ID, &start, &end, &group.CoverItemID, &group.ItemCount); err != nil {
				_ = yearRows.Close()
				return err
			}
			group.Kind, group.Title, group.Subtitle, group.StartAt, group.EndAt = "year", start.Format("2006"), fmt.Sprintf("%d items", group.ItemCount), &start, &end
			out.Years = append(out.Years, group)
		}
		if err := yearRows.Close(); err != nil {
			return err
		}

		memoryRows, err := tx.QueryContext(ctx, `SELECT to_char(bucket,'YYYY-MM'),bucket,max(captured_at),(array_agg(id ORDER BY captured_at DESC,id))[1],count(*)
			FROM (SELECT i.id,date_trunc('month',COALESCE(i.date_override,f.intrinsic_capture_at,f.original_uploaded_at)) bucket,COALESCE(i.date_override,f.intrinsic_capture_at,f.original_uploaded_at) captured_at
			FROM space_library_items i JOIN library_files f ON f.id=i.file_id WHERE i.space_id=$1 AND i.lifecycle_state='ready' AND i.hidden=FALSE) visible
			GROUP BY bucket HAVING count(*)>=2 ORDER BY bucket DESC LIMIT 24`, spaceID)
		if err != nil {
			return err
		}
		for memoryRows.Next() {
			var group LibraryDiscoveryGroup
			var start, end time.Time
			if err := memoryRows.Scan(&group.ID, &start, &end, &group.CoverItemID, &group.ItemCount); err != nil {
				_ = memoryRows.Close()
				return err
			}
			group.Kind, group.Title, group.Subtitle, group.StartAt, group.EndAt = "memory", start.Format("January 2006"), fmt.Sprintf("%d items", group.ItemCount), &start, &end
			out.Memories = append(out.Memories, group)
		}
		if err := memoryRows.Close(); err != nil {
			return err
		}
		preferenceRows, err := tx.QueryContext(ctx, `SELECT memory_id,title,COALESCE(cover_item_id,''),COALESCE(music_item_id,''),playback_seconds,version FROM space_memory_preferences WHERE space_id=$1`, spaceID)
		if err != nil {
			return err
		}
		for preferenceRows.Next() {
			var memoryID, title, coverItemID, musicItemID string
			var playbackSeconds float64
			var version int64
			if err := preferenceRows.Scan(&memoryID, &title, &coverItemID, &musicItemID, &playbackSeconds, &version); err != nil {
				_ = preferenceRows.Close()
				return err
			}
			for index := range out.Memories {
				if out.Memories[index].ID != memoryID {
					continue
				}
				if title != "" {
					out.Memories[index].Title = title
				}
				if coverItemID != "" {
					out.Memories[index].CoverItemID = coverItemID
				}
				out.Memories[index].MusicItemID = musicItemID
				out.Memories[index].PlaybackSeconds = playbackSeconds
				out.Memories[index].PreferenceVersion = version
				break
			}
		}
		if err := preferenceRows.Close(); err != nil {
			return err
		}

		tripRows, err := tx.QueryContext(ctx, `SELECT COALESCE(i.location_override,f.intrinsic_location)->>'name',min(COALESCE(i.date_override,f.intrinsic_capture_at,f.original_uploaded_at)),max(COALESCE(i.date_override,f.intrinsic_capture_at,f.original_uploaded_at)),(array_agg(i.id ORDER BY COALESCE(i.date_override,f.intrinsic_capture_at,f.original_uploaded_at) DESC,i.id))[1],count(*)
			FROM space_library_items i JOIN library_files f ON f.id=i.file_id
			WHERE i.space_id=$1 AND i.lifecycle_state='ready' AND i.hidden=FALSE AND length(trim(COALESCE(COALESCE(i.location_override,f.intrinsic_location)->>'name','')))>0
			GROUP BY lower(COALESCE(i.location_override,f.intrinsic_location)->>'name'),COALESCE(i.location_override,f.intrinsic_location)->>'name' HAVING count(*)>=2 ORDER BY max(COALESCE(i.date_override,f.intrinsic_capture_at,f.original_uploaded_at)) DESC LIMIT 24`, spaceID)
		if err != nil {
			return err
		}
		for tripRows.Next() {
			var group LibraryDiscoveryGroup
			var start, end time.Time
			if err := tripRows.Scan(&group.ID, &start, &end, &group.CoverItemID, &group.ItemCount); err != nil {
				_ = tripRows.Close()
				return err
			}
			group.Kind, group.Title, group.Subtitle, group.StartAt, group.EndAt = "trip", group.ID, libraryDateRange(start, end), &start, &end
			out.Trips = append(out.Trips, group)
		}
		if err := tripRows.Close(); err != nil {
			return err
		}

		duplicateRows, err := tx.QueryContext(ctx, `SELECT min(i.id),min(i.id),(array_agg(i.id ORDER BY i.added_at DESC,i.id))[1],count(*)
			FROM space_library_items i JOIN library_files f ON f.id=i.file_id
			WHERE i.space_id=$1 AND i.lifecycle_state='ready' AND i.hidden=FALSE
			GROUP BY f.blob_id HAVING count(*)>1 ORDER BY count(*) DESC,max(i.added_at) DESC LIMIT 100`, spaceID)
		if err != nil {
			return err
		}
		for duplicateRows.Next() {
			var group LibraryDiscoveryGroup
			if err := duplicateRows.Scan(&group.ID, &group.Title, &group.CoverItemID, &group.ItemCount); err != nil {
				_ = duplicateRows.Close()
				return err
			}
			group.Kind, group.Title, group.Subtitle = "duplicate", "Duplicates", fmt.Sprintf("%d matching items", group.ItemCount)
			out.Duplicates = append(out.Duplicates, group)
		}
		return duplicateRows.Close()
	})
	return out, err
}
