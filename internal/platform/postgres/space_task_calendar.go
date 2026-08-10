package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

// TaskSchedule holds the fields Google Calendar is the source of truth for.
type TaskSchedule struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Location    string `json:"location"`
	StartsAt    string `json:"starts_at"`
	EndsAt      string `json:"ends_at"`
	AllDay      bool   `json:"all_day"`
	Timezone    string `json:"timezone"`
}

// TaskCalendarLink binds a task to a Google calendar.
//
// Published is the crux of the model: it snapshots the schedule as last agreed
// with Google. Comparing the live schedule against it reveals local edits;
// comparing an incoming event against it reveals remote edits. When both differ
// on the same field, that is a real conflict rather than a stale write.
type TaskCalendarLink struct {
	SourceID         string        `json:"source_id"`
	GoogleCalendarID string        `json:"google_calendar_id"`
	GoogleEventID    string        `json:"google_event_id,omitempty"`
	Published        *TaskSchedule `json:"published,omitempty"`
	PublishedAt      string        `json:"published_at,omitempty"`
	RemoteUpdatedAt  string        `json:"remote_updated_at,omitempty"`
	CanceledAt       string        `json:"canceled_at,omitempty"`
	LastErrorCode    string        `json:"last_error_code,omitempty"`
}

// ScheduleFields are compared field by field when merging a Google update.
var ScheduleFields = []string{"title", "description", "location", "starts_at", "ends_at", "all_day", "timezone"}

func scheduleField(schedule TaskSchedule, field string) string {
	switch field {
	case "title":
		return schedule.Title
	case "description":
		return schedule.Description
	case "location":
		return schedule.Location
	case "starts_at":
		return schedule.StartsAt
	case "ends_at":
		return schedule.EndsAt
	case "all_day":
		if schedule.AllDay {
			return "true"
		}
		return "false"
	default:
		return schedule.Timezone
	}
}

func assignScheduleField(target *TaskSchedule, source TaskSchedule, field string) {
	switch field {
	case "title":
		target.Title = source.Title
	case "description":
		target.Description = source.Description
	case "location":
		target.Location = source.Location
	case "starts_at":
		target.StartsAt = source.StartsAt
	case "ends_at":
		target.EndsAt = source.EndsAt
	case "all_day":
		target.AllDay = source.AllDay
	default:
		target.Timezone = source.Timezone
	}
}

// UnpublishedFields lists where the live schedule differs from what Google
// last agreed to.
func UnpublishedFields(schedule TaskSchedule, link *TaskCalendarLink) []string {
	if link == nil || link.Published == nil {
		return nil
	}
	fields := []string{}
	for _, field := range ScheduleFields {
		if scheduleField(schedule, field) != scheduleField(*link.Published, field) {
			fields = append(fields, field)
		}
	}
	return fields
}

// MergeGoogleSchedule folds an incoming Google schedule into a task.
//
// Fields only Google changed are applied. Fields only Misty changed stay put
// and remain unpublished. Fields both changed keep the local value and are
// reported as conflicts, so the user chooses instead of losing an edit.
func MergeGoogleSchedule(local TaskSchedule, link TaskCalendarLink, incoming TaskSchedule) (TaskSchedule, TaskCalendarLink, []string) {
	localChanged := map[string]bool{}
	for _, field := range UnpublishedFields(local, &link) {
		localChanged[field] = true
	}
	published := TaskSchedule{}
	if link.Published != nil {
		published = *link.Published
	} else {
		published = incoming
	}

	merged, conflicts := local, []string{}
	for _, field := range ScheduleFields {
		if link.Published != nil && scheduleField(incoming, field) == scheduleField(*link.Published, field) {
			continue
		}
		if localChanged[field] {
			conflicts = append(conflicts, field)
			continue
		}
		assignScheduleField(&merged, incoming, field)
		assignScheduleField(&published, incoming, field)
	}
	link.Published = &published
	return merged, link, conflicts
}

// CreateCalendarSpaceTask creates a task bound to a Google calendar. A task
// with no google_event_id is a local draft until it is published.
func (db *Database) CreateCalendarSpaceTask(ctx context.Context, userID string, item SpaceTask, schedule TaskSchedule, link TaskCalendarLink) (*SpaceTask, error) {
	created, err := db.CreateSpaceTask(ctx, userID, item)
	if err != nil {
		return nil, err
	}
	return db.SetSpaceTaskCalendar(ctx, created.SpaceID, created.ID, &schedule, &link, nil)
}

// SetSpaceTaskCalendar writes the calendar columns. It is service-side: sync
// and publish both need to record provider state without a member's request.
func (db *Database) SetSpaceTaskCalendar(ctx context.Context, spaceID, taskID string, schedule *TaskSchedule, link *TaskCalendarLink, conflicts []string) (*SpaceTask, error) {
	out := &SpaceTask{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		var scheduleRaw, linkRaw any
		if schedule != nil {
			encoded, _ := json.Marshal(schedule)
			scheduleRaw = encoded
		}
		if link != nil {
			encoded, _ := json.Marshal(link)
			linkRaw = encoded
		}
		if conflicts == nil {
			conflicts = []string{}
		}
		err := scanSpaceTask(tx.QueryRowContext(ctx, `UPDATE space_tasks
			SET schedule=$1,calendar=$2,conflicted_fields=$3,version=version+1,updated_at=NOW()
			WHERE id=$4 AND space_id=$5 RETURNING `+spaceTaskColumns,
			scheduleRaw, linkRaw, pq.Array(conflicts), taskID, spaceID), out)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrSpaceNotFound
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// SpaceTaskByID reads one task for a service-side calendar operation.
func (db *Database) SpaceTaskByID(ctx context.Context, spaceID, taskID string) (*SpaceTask, error) {
	out := &SpaceTask{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		return scanSpaceTask(tx.QueryRowContext(ctx, `SELECT `+spaceTaskColumns+`
			FROM space_tasks WHERE id=$1 AND space_id=$2`, taskID, spaceID), out)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceNotFound
	}
	if err != nil {
		return nil, err
	}
	return out, nil
}

// CalendarBackedTasks lists the tasks bound to one calendar source, so a sync
// pass can reconcile them against the events Google returned.
func (db *Database) CalendarBackedTasks(ctx context.Context, spaceID, sourceID string) ([]SpaceTask, error) {
	items := []SpaceTask{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT `+spaceTaskColumns+` FROM space_tasks
			WHERE space_id=$1 AND calendar->>'source_id'=$2 AND archived_at IS NULL`, spaceID, sourceID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item SpaceTask
			if err := scanSpaceTask(rows, &item); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}

// TaskSchedule decodes the stored schedule, if the task has one.
func (t SpaceTask) TaskSchedule() *TaskSchedule {
	if len(t.Schedule) == 0 {
		return nil
	}
	var schedule TaskSchedule
	if json.Unmarshal(t.Schedule, &schedule) != nil {
		return nil
	}
	return &schedule
}

// TaskCalendarLink decodes the stored calendar binding, if the task has one.
func (t SpaceTask) TaskCalendarLink() *TaskCalendarLink {
	if len(t.Calendar) == 0 {
		return nil
	}
	var link TaskCalendarLink
	if json.Unmarshal(t.Calendar, &link) != nil {
		return nil
	}
	return &link
}

// NewCalendarTaskID mints the identifier Google will echo back as the event id.
// Google requires lowercase base32hex, so a UUID is normalized rather than sent
// verbatim.
func NewCalendarTaskID() string {
	return "misty" + strings.ReplaceAll(uuid.NewString(), "-", "")
}

// TaskScheduleFromTimes builds a schedule from parsed event times.
func TaskScheduleFromTimes(title, description, location string, startsAt, endsAt time.Time, allDay bool, timezone string) TaskSchedule {
	format := time.RFC3339
	if allDay {
		format = "2006-01-02"
	}
	if timezone == "" {
		timezone = "UTC"
	}
	return TaskSchedule{
		Title: title, Description: description, Location: location,
		StartsAt: startsAt.UTC().Format(format), EndsAt: endsAt.UTC().Format(format),
		AllDay: allDay, Timezone: timezone,
	}
}

// SpaceCalendarEventsForSource lists one source's events, including the ones
// Google removed or canceled.
//
// The member-facing SpaceCalendarEvents deliberately hides removed rows, but
// reconciliation needs exactly those: a task whose event disappeared must be
// marked canceled rather than silently drifting out of sync.
func (db *Database) SpaceCalendarEventsForSource(ctx context.Context, spaceID, sourceID string, from, to time.Time) ([]SpaceCalendarEvent, error) {
	out := []SpaceCalendarEvent{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT id,space_id,source_id,provider,external_event_id,fingerprint,title,description,location,meeting_url,organizer,starts_at,ends_at,all_day,timezone,status,provider_created_at,provider_updated_at,removed_at,created_at,updated_at
			FROM space_calendar_events WHERE space_id=$1 AND source_id=$2 AND starts_at<$3 AND ends_at>$4 ORDER BY starts_at,id`, spaceID, sourceID, to, from)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item SpaceCalendarEvent
			if err := rows.Scan(&item.ID, &item.SpaceID, &item.SourceID, &item.Provider, &item.ExternalEventID, &item.Fingerprint, &item.Title, &item.Description, &item.Location, &item.MeetingURL, &item.Organizer, &item.StartsAt, &item.EndsAt, &item.AllDay, &item.Timezone, &item.Status, &item.ProviderCreatedAt, &item.ProviderUpdatedAt, &item.RemovedAt, &item.CreatedAt, &item.UpdatedAt); err != nil {
				return err
			}
			out = append(out, item)
		}
		return rows.Err()
	})
	return out, err
}
