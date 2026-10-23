package db

import (
	"errors"
	"time"
)

// ScheduleTiming is when a workflow runs: any number of rules in one timezone,
// minus skipped dates. A run happens at every moment any rule produces.
type ScheduleTiming struct {
	Timezone  string         `json:"timezone"`
	Rules     []ScheduleRule `json:"rules"`
	SkipDates []string       `json:"skip_dates"`
}

// ScheduleRule is one repeating pattern, close to an iCalendar RRULE:
//   - once: each of Dates at each of Times.
//   - hourly: every Interval hours at Minute, optionally only between From and Until.
//   - daily: every Interval days at Times.
//   - weekly: every Interval weeks on Weekdays at Times.
//   - monthly: every Interval months on MonthDays or NthWeekdays at Times.
//   - yearly: every Interval years in Months, on MonthDays or NthWeekdays, at Times.
//
// Weekdays also narrows hourly and daily rules ("every 2 hours on weekdays").
// Intervals count from StartDate; EndDate is the last day a rule can run.
type ScheduleRule struct {
	Frequency   string       `json:"frequency"`
	Interval    int          `json:"interval,omitempty"`
	Dates       []string     `json:"dates,omitempty"`
	Times       []string     `json:"times,omitempty"`
	Minute      int          `json:"minute,omitempty"`
	From        string       `json:"from,omitempty"`
	Until       string       `json:"until,omitempty"`
	Weekdays    []int        `json:"weekdays,omitempty"`
	MonthDays   []int        `json:"month_days,omitempty"`
	NthWeekdays []NthWeekday `json:"nth_weekdays,omitempty"`
	Months      []int        `json:"months,omitempty"`
	StartDate   string       `json:"start_date,omitempty"`
	EndDate     string       `json:"end_date,omitempty"`
}

// NthWeekday is "the second Tuesday" (Nth 2, Weekday 2) or "the last Friday" (Nth -1).
type NthWeekday struct {
	Nth     int `json:"nth"`
	Weekday int `json:"weekday"`
}

var errInvalidSchedule = errors.New("invalid schedule")

const (
	maxScheduleRules     = 20
	maxScheduleTimes     = 24
	maxScheduleDates     = 100
	maxScheduleSkipDates = 366
	// Long enough for a yearly rule with the largest interval to recur.
	scheduleSearchDays = 4000
)

var maxIntervals = map[string]int{"hourly": 23, "daily": 365, "weekly": 52, "monthly": 24, "yearly": 10}

// Normalize fills defaults: an interval of 1, and today as the anchor for rules
// without a start date, so "every 2 weeks" counts from when it was saved.
func (s *ScheduleTiming) Normalize(now time.Time) {
	location, err := scheduleLocation(s.Timezone)
	if err != nil {
		return
	}
	today := now.In(location).Format(time.DateOnly)
	if s.SkipDates == nil {
		s.SkipDates = []string{}
	}
	for i := range s.Rules {
		rule := &s.Rules[i]
		if rule.Frequency == "once" {
			continue
		}
		if rule.Interval == 0 {
			rule.Interval = 1
		}
		if rule.StartDate == "" {
			rule.StartDate = today
		}
	}
}

// Validate reports whether the timing can be stored and evaluated.
func (s ScheduleTiming) Validate() error {
	if _, err := scheduleLocation(s.Timezone); err != nil {
		return err
	}
	if len(s.Rules) == 0 || len(s.Rules) > maxScheduleRules || len(s.SkipDates) > maxScheduleSkipDates {
		return errInvalidSchedule
	}
	for _, day := range s.SkipDates {
		if !validDate(day) {
			return errInvalidSchedule
		}
	}
	for _, rule := range s.Rules {
		if err := rule.validate(); err != nil {
			return err
		}
	}
	return nil
}

func (r ScheduleRule) validate() error {
	inRange := func(values []int, low, high int) bool {
		for _, v := range values {
			if v < low || v > high {
				return false
			}
		}
		return true
	}
	if !inRange(r.Weekdays, 0, 6) || !inRange(r.Months, 1, 12) || len(r.Weekdays) > 7 || len(r.Months) > 12 || len(r.MonthDays) > 32 || len(r.NthWeekdays) > 35 {
		return errInvalidSchedule
	}
	for _, day := range r.MonthDays {
		if day != -1 && (day < 1 || day > 31) {
			return errInvalidSchedule
		}
	}
	for _, nth := range r.NthWeekdays {
		if nth.Weekday < 0 || nth.Weekday > 6 || (nth.Nth != -1 && (nth.Nth < 1 || nth.Nth > 5)) {
			return errInvalidSchedule
		}
	}
	if r.Frequency == "once" {
		if len(r.Dates) == 0 || len(r.Dates) > maxScheduleDates || !validTimes(r.Times) {
			return errInvalidSchedule
		}
		for _, day := range r.Dates {
			if !validDate(day) {
				return errInvalidSchedule
			}
		}
		return nil
	}
	limit, ok := maxIntervals[r.Frequency]
	if !ok || r.Interval < 1 || r.Interval > limit || !validDate(r.StartDate) {
		return errInvalidSchedule
	}
	if r.EndDate != "" && (!validDate(r.EndDate) || r.EndDate < r.StartDate) {
		return errInvalidSchedule
	}
	days := len(r.MonthDays) > 0 || len(r.NthWeekdays) > 0
	switch r.Frequency {
	case "hourly":
		if r.Minute < 0 || r.Minute > 59 || (r.From != "" && !validClock(r.From)) || (r.Until != "" && !validClock(r.Until)) {
			return errInvalidSchedule
		}
		if r.From != "" && r.Until != "" && r.Until < r.From {
			return errInvalidSchedule
		}
		return nil
	case "weekly":
		if len(r.Weekdays) == 0 {
			return errInvalidSchedule
		}
	case "monthly":
		if !days {
			return errInvalidSchedule
		}
	case "yearly":
		if !days || len(r.Months) == 0 {
			return errInvalidSchedule
		}
	}
	if !validTimes(r.Times) {
		return errInvalidSchedule
	}
	return nil
}

func validTimes(times []string) bool {
	if len(times) == 0 || len(times) > maxScheduleTimes {
		return false
	}
	for _, value := range times {
		if !validClock(value) {
			return false
		}
	}
	return true
}

func validClock(value string) bool {
	if len(value) != 5 || value[2] != ':' {
		return false
	}
	_, err := time.Parse("15:04", value)
	return err == nil
}

func validDate(value string) bool {
	_, err := time.Parse(time.DateOnly, value)
	return err == nil
}
