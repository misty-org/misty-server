package db

import (
	"errors"
	"strings"
	"time"
)

// ScheduledTaskSchedule is when a scheduled task runs, in the owner's own timezone.
type ScheduledTaskSchedule struct {
	Cadence   string `json:"cadence"`
	LocalTime string `json:"local_time"`
	Weekday   int    `json:"weekday"`
	MonthDay  int    `json:"month_day"`
	// RunOn is the calendar date (YYYY-MM-DD) of a one-time task.
	RunOn    string `json:"run_on,omitempty"`
	Timezone string `json:"timezone"`
}

var errInvalidSchedule = errors.New("invalid schedule")

// Validate reports whether the schedule can be stored and evaluated.
func (s ScheduledTaskSchedule) Validate() error {
	switch s.Cadence {
	case "once", "daily", "weekdays", "weekly", "monthly":
	default:
		return errInvalidSchedule
	}
	if len(s.LocalTime) != 5 || s.LocalTime[2] != ':' {
		return errInvalidSchedule
	}
	if _, err := time.Parse("15:04", s.LocalTime); err != nil {
		return errInvalidSchedule
	}
	if s.Weekday < 0 || s.Weekday > 6 || s.MonthDay < 1 || s.MonthDay > 31 {
		return errInvalidSchedule
	}
	if s.Cadence == "once" {
		if _, err := time.Parse(time.DateOnly, s.RunOn); err != nil {
			return errInvalidSchedule
		}
	}
	if _, err := scheduleLocation(s.Timezone); err != nil {
		return err
	}
	return nil
}

// NextRun returns the first run strictly after `after`, or ok=false when a one-time task's
// moment has passed.
func (s ScheduledTaskSchedule) NextRun(after time.Time) (time.Time, bool, error) {
	if err := s.Validate(); err != nil {
		return time.Time{}, false, err
	}
	location, _ := scheduleLocation(s.Timezone)
	clock, _ := time.Parse("15:04", s.LocalTime)
	local := after.In(location)
	at := func(year int, month time.Month, day int) time.Time {
		return time.Date(year, month, day, clock.Hour(), clock.Minute(), 0, 0, location)
	}
	if s.Cadence == "once" {
		day, _ := time.Parse(time.DateOnly, s.RunOn)
		candidate := at(day.Year(), day.Month(), day.Day())
		if !candidate.After(after) {
			return time.Time{}, false, nil
		}
		return candidate.UTC(), true, nil
	}
	if s.Cadence == "monthly" {
		for offset := 0; offset < 3; offset++ {
			first := time.Date(local.Year(), local.Month()+time.Month(offset), 1, 0, 0, 0, 0, location)
			// Short months run on their last day rather than skipping.
			day := min(s.MonthDay, first.AddDate(0, 1, -1).Day())
			candidate := at(first.Year(), first.Month(), day)
			if candidate.After(after) {
				return candidate.UTC(), true, nil
			}
		}
		return time.Time{}, false, errInvalidSchedule
	}
	for offset := 0; offset <= 7; offset++ {
		day := local.AddDate(0, 0, offset)
		candidate := at(day.Year(), day.Month(), day.Day())
		if !candidate.After(after) {
			continue
		}
		weekday := int(candidate.Weekday())
		switch s.Cadence {
		case "weekdays":
			if weekday == 0 || weekday == 6 {
				continue
			}
		case "weekly":
			if weekday != s.Weekday {
				continue
			}
		}
		return candidate.UTC(), true, nil
	}
	return time.Time{}, false, errInvalidSchedule
}

func scheduleLocation(timezone string) (*time.Location, error) {
	name := strings.TrimSpace(timezone)
	if name == "" || name == "local" || len(name) > 100 {
		return nil, errInvalidSchedule
	}
	location, err := time.LoadLocation(name)
	if err != nil || location.String() == "Local" {
		return nil, errInvalidSchedule
	}
	return location, nil
}
