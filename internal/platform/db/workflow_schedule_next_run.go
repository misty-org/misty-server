package db

import (
	"slices"
	"strings"
	"time"
)

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
