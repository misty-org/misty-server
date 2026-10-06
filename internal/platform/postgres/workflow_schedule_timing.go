package db

import (
	"errors"
	"slices"
	"strings"
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

// NextRun returns the first run strictly after `after`, or ok=false when no rule
// produces another run.
func (s ScheduleTiming) NextRun(after time.Time) (time.Time, bool, error) {
	if err := s.Validate(); err != nil {
		return time.Time{}, false, err
	}
	location, _ := scheduleLocation(s.Timezone)
	skip := map[string]bool{}
	for _, day := range s.SkipDates {
		skip[day] = true
	}
	var best time.Time
	for _, rule := range s.Rules {
		if next, ok := rule.next(after, location, skip); ok && (best.IsZero() || next.Before(best)) {
			best = next
		}
	}
	if best.IsZero() {
		return time.Time{}, false, nil
	}
	return best.UTC(), true, nil
}

func (r ScheduleRule) next(after time.Time, location *time.Location, skip map[string]bool) (time.Time, bool) {
	if r.Frequency == "once" {
		var best time.Time
		for _, day := range r.Dates {
			if skip[day] {
				continue
			}
			date, _ := time.Parse(time.DateOnly, day)
			for _, at := range r.at(date, location, 0) {
				if at.After(after) && (best.IsZero() || at.Before(best)) {
					best = at
				}
			}
		}
		return best, !best.IsZero()
	}
	start, _ := time.Parse(time.DateOnly, r.StartDate)
	local := after.In(location)
	day := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC)
	if day.Before(start) {
		day = start
	}
	end := time.Time{}
	if r.EndDate != "" {
		end, _ = time.Parse(time.DateOnly, r.EndDate)
	}
	for range scheduleSearchDays {
		if !end.IsZero() && day.After(end) {
			break
		}
		elapsed := int(day.Sub(start).Hours() / 24)
		if !skip[day.Format(time.DateOnly)] && r.matches(day, start, elapsed) {
			for _, at := range r.at(day, location, elapsed) {
				if at.After(after) {
					return at, true
				}
			}
		}
		day = day.AddDate(0, 0, 1)
	}
	return time.Time{}, false
}

// matches reports whether a calendar day (UTC midnight) is one the rule runs on.
func (r ScheduleRule) matches(day, start time.Time, elapsed int) bool {
	if len(r.Weekdays) > 0 && !slices.Contains(r.Weekdays, int(day.Weekday())) {
		return false
	}
	switch r.Frequency {
	case "hourly":
		return true
	case "daily":
		return elapsed%r.Interval == 0
	case "weekly":
		// Weeks start on Sunday, counted from the start date's week.
		return ((elapsed+int(start.Weekday()))/7)%r.Interval == 0
	case "monthly":
		months := (day.Year()-start.Year())*12 + int(day.Month()-start.Month())
		return months%r.Interval == 0 && r.dayOfMonth(day)
	case "yearly":
		return (day.Year()-start.Year())%r.Interval == 0 && slices.Contains(r.Months, int(day.Month())) && r.dayOfMonth(day)
	}
	return false
}

// dayOfMonth: a day past the month's end runs on its last day, and -1 is always
// the last day; NthWeekdays pick "the second Tuesday" or "the last Friday".
func (r ScheduleRule) dayOfMonth(day time.Time) bool {
	last := time.Date(day.Year(), day.Month()+1, 0, 0, 0, 0, 0, time.UTC).Day()
	for _, want := range r.MonthDays {
		if (want == -1 && day.Day() == last) || (want > 0 && min(want, last) == day.Day()) {
			return true
		}
	}
	for _, nth := range r.NthWeekdays {
		if int(day.Weekday()) != nth.Weekday {
			continue
		}
		if (nth.Nth == -1 && day.Day()+7 > last) || (nth.Nth > 0 && (day.Day()-1)/7+1 == nth.Nth) {
			return true
		}
	}
	return false
}

// at lists a matching day's run moments in order.
func (r ScheduleRule) at(day time.Time, location *time.Location, elapsed int) []time.Time {
	out := []time.Time{}
	if r.Frequency == "hourly" {
		for hour := range 24 {
			clock := hourClock(hour, r.Minute)
			if (elapsed*24+hour)%r.Interval != 0 || (r.From != "" && clock < r.From) || (r.Until != "" && clock > r.Until) {
				continue
			}
			out = append(out, time.Date(day.Year(), day.Month(), day.Day(), hour, r.Minute, 0, 0, location))
		}
		return out
	}
	times := slices.Clone(r.Times)
	slices.Sort(times)
	for _, value := range times {
		clock, _ := time.Parse("15:04", value)
		out = append(out, time.Date(day.Year(), day.Month(), day.Day(), clock.Hour(), clock.Minute(), 0, 0, location))
	}
	return out
}

func hourClock(hour, minute int) string {
	return time.Date(2000, 1, 1, hour, minute, 0, 0, time.UTC).Format("15:04")
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
