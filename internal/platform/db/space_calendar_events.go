package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// SpaceCalendarEvent is an event on a Space's own calendar. Events from a
// person's connected calendars never reach the server: their computer reads them.
type SpaceCalendarEvent struct {
	ID              string    `json:"id"`
	SpaceID         string    `json:"space_id"`
	Title           string    `json:"title"`
	Description     string    `json:"description"`
	Location        string    `json:"location"`
	StartsAt        time.Time `json:"starts_at"`
	EndsAt          time.Time `json:"ends_at"`
	AllDay          bool      `json:"all_day"`
	Timezone        string    `json:"timezone"`
	CreatedByUserID string    `json:"created_by_user_id"`
	Version         int64     `json:"version"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// SpaceCalendarEventInput is what a person sets on an event.
type SpaceCalendarEventInput struct {
	Title       string
	Description string
	Location    string
	StartsAt    time.Time
	EndsAt      time.Time
	AllDay      bool
	Timezone    string
}

const spaceCalendarEventColumns = `id,space_id,title,description,location,starts_at,ends_at,
	all_day,timezone,created_by_user_id,version,created_at,updated_at`

func scanSpaceCalendarEvent(row interface{ Scan(...any) error }, out *SpaceCalendarEvent) error {
	return row.Scan(
		&out.ID, &out.SpaceID, &out.Title, &out.Description, &out.Location, &out.StartsAt,
		&out.EndsAt, &out.AllDay, &out.Timezone, &out.CreatedByUserID, &out.Version,
		&out.CreatedAt, &out.UpdatedAt,
	)
}

func normalizeCalendarEventInput(input SpaceCalendarEventInput) (SpaceCalendarEventInput, error) {
	input.Title = strings.TrimSpace(input.Title)
	input.Location = strings.TrimSpace(input.Location)
	input.Timezone = strings.TrimSpace(input.Timezone)
	if input.Timezone == "" {
		input.Timezone = "UTC"
	}
	if _, err := time.LoadLocation(input.Timezone); err != nil {
		return input, ErrSpaceInvalid
	}
	if input.Title == "" || utf8.RuneCountInString(input.Title) > 240 ||
		utf8.RuneCountInString(input.Description) > 20000 ||
		utf8.RuneCountInString(input.Location) > 500 ||
		input.StartsAt.IsZero() || input.EndsAt.Before(input.StartsAt) {
		return input, ErrSpaceInvalid
	}
	return input, nil
}

func recordCalendarEventTx(ctx context.Context, tx *sql.Tx, spaceID, actorUserID, eventType, eventID string) error {
	_, err := recordSpaceEventTx(ctx, tx, spaceID, actorUserID, eventType, eventID,
		map[string]any{"event_id": eventID})
	return err
}

// SpaceCalendarEvents lists the events that overlap [from, to).
func (db *Database) SpaceCalendarEvents(ctx context.Context, userID, spaceID string, from, to time.Time) ([]SpaceCalendarEvent, error) {
	if !to.After(from) || to.Sub(from) > 400*24*time.Hour {
		return nil, ErrSpaceInvalid
	}
	events := []SpaceCalendarEvent{}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT `+spaceCalendarEventColumns+` FROM space_calendar_events
			WHERE space_id=$1 AND starts_at<$3 AND ends_at>=$2 ORDER BY starts_at,id LIMIT 2000`, spaceID, from, to)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var event SpaceCalendarEvent
			if err := scanSpaceCalendarEvent(rows, &event); err != nil {
				return err
			}
			events = append(events, event)
		}
		return rows.Err()
	})
	return events, err
}

// CreateSpaceCalendarEvent adds an event to a Space's calendar.
func (db *Database) CreateSpaceCalendarEvent(ctx context.Context, userID, spaceID string, input SpaceCalendarEventInput) (*SpaceCalendarEvent, error) {
	input, err := normalizeCalendarEventInput(input)
	if err != nil {
		return nil, err
	}
	event := &SpaceCalendarEvent{}
	err = db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		if err := scanSpaceCalendarEvent(tx.QueryRowContext(ctx, `INSERT INTO space_calendar_events(
				id,space_id,title,description,location,starts_at,ends_at,all_day,timezone,created_by_user_id)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING `+spaceCalendarEventColumns,
			"event_"+uuid.NewString(), spaceID, input.Title, input.Description, input.Location,
			input.StartsAt.UTC(), input.EndsAt.UTC(), input.AllDay, input.Timezone, userID), event); err != nil {
			return err
		}
		return recordCalendarEventTx(ctx, tx, spaceID, userID, "calendar.event_created", event.ID)
	})
	if err != nil {
		return nil, err
	}
	return event, nil
}

// UpdateSpaceCalendarEvent replaces an event's fields; the last write wins.
func (db *Database) UpdateSpaceCalendarEvent(ctx context.Context, userID, spaceID, eventID string, input SpaceCalendarEventInput) (*SpaceCalendarEvent, error) {
	input, err := normalizeCalendarEventInput(input)
	if err != nil {
		return nil, err
	}
	event := &SpaceCalendarEvent{}
	err = db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		err := scanSpaceCalendarEvent(tx.QueryRowContext(ctx, `UPDATE space_calendar_events SET
				title=$3,description=$4,location=$5,starts_at=$6,ends_at=$7,all_day=$8,timezone=$9,
				version=version+1,updated_at=NOW()
			WHERE id=$1 AND space_id=$2 RETURNING `+spaceCalendarEventColumns,
			eventID, spaceID, input.Title, input.Description, input.Location,
			input.StartsAt.UTC(), input.EndsAt.UTC(), input.AllDay, input.Timezone), event)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrSpaceNotFound
		}
		if err != nil {
			return err
		}
		return recordCalendarEventTx(ctx, tx, spaceID, userID, "calendar.event_updated", eventID)
	})
	if err != nil {
		return nil, err
	}
	return event, nil
}

// DeleteSpaceCalendarEvent removes an event from a Space's calendar.
func (db *Database) DeleteSpaceCalendarEvent(ctx context.Context, userID, spaceID, eventID string) error {
	return db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `DELETE FROM space_calendar_events WHERE id=$1 AND space_id=$2`, eventID, spaceID)
		if err != nil {
			return err
		}
		if deleted, _ := result.RowsAffected(); deleted == 0 {
			return ErrSpaceNotFound
		}
		return recordCalendarEventTx(ctx, tx, spaceID, userID, "calendar.event_deleted", eventID)
	})
}
