package db

import (
	"testing"
	"time"
)

func TestScheduleTimingNextRun(t *testing.T) {
	// Friday 2026-09-25 10:00 in Los Angeles.
	now := time.Date(2026, 9, 25, 17, 0, 0, 0, time.UTC)
	la := "America/Los_Angeles"
	rule := func(r ScheduleRule) ScheduleTiming {
		if r.StartDate == "" && r.Frequency != "once" {
			r.StartDate = "2026-09-01"
		}
		if r.Interval == 0 && r.Frequency != "once" {
			r.Interval = 1
		}
		return ScheduleTiming{Timezone: la, Rules: []ScheduleRule{r}}
	}
	cases := []struct {
		name   string
		timing ScheduleTiming
		want   string
	}{
		{"daily later today", rule(ScheduleRule{Frequency: "daily", Times: []string{"11:30"}}), "2026-09-25T11:30"},
		{"daily already passed", rule(ScheduleRule{Frequency: "daily", Times: []string{"09:00"}}), "2026-09-26T09:00"},
		{"several times a day", rule(ScheduleRule{Frequency: "daily", Times: []string{"17:00", "09:00"}}), "2026-09-25T17:00"},
		{"every other day from the start", rule(ScheduleRule{Frequency: "daily", Interval: 2, Times: []string{"09:00"}}), "2026-09-27T09:00"},
		{"weekdays skip the weekend", rule(ScheduleRule{Frequency: "weekly", Weekdays: []int{1, 2, 3, 4, 5}, Times: []string{"09:00"}}), "2026-09-28T09:00"},
		{"every two weeks", rule(ScheduleRule{Frequency: "weekly", Interval: 2, Weekdays: []int{4}, Times: []string{"09:00"}}), "2026-10-01T09:00"},
		{"monthly clamps to a short month", rule(ScheduleRule{Frequency: "monthly", MonthDays: []int{31}, Times: []string{"08:00"}}), "2026-09-30T08:00"},
		{"last day of the month", rule(ScheduleRule{Frequency: "monthly", MonthDays: []int{-1}, Times: []string{"08:00"}}), "2026-09-30T08:00"},
		{"first Monday", rule(ScheduleRule{Frequency: "monthly", NthWeekdays: []NthWeekday{{Nth: 1, Weekday: 1}}, Times: []string{"08:00"}}), "2026-10-05T08:00"},
		{"last Friday", rule(ScheduleRule{Frequency: "monthly", NthWeekdays: []NthWeekday{{Nth: -1, Weekday: 5}}, Times: []string{"12:00"}}), "2026-09-25T12:00"},
		{"yearly in a month", rule(ScheduleRule{Frequency: "yearly", Months: []int{1}, MonthDays: []int{1}, Times: []string{"00:05"}}), "2027-01-01T00:05"},
		{"hourly within a window", rule(ScheduleRule{Frequency: "hourly", Interval: 3, Minute: 15, From: "09:00", Until: "17:00", Weekdays: []int{5}}), "2026-09-25T12:15"},
		{"once in the future", rule(ScheduleRule{Frequency: "once", Dates: []string{"2026-10-01", "2026-09-30"}, Times: []string{"12:00"}}), "2026-09-30T12:00"},
	}
	location, _ := time.LoadLocation(la)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			next, ok, err := tc.timing.NextRun(now)
			if err != nil || !ok {
				t.Fatalf("NextRun: %v %v", ok, err)
			}
			if got := next.In(location).Format("2006-01-02T15:04"); got != tc.want {
				t.Fatalf("next = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestScheduleTimingCombinesRulesAndSkips(t *testing.T) {
	now := time.Date(2026, 9, 25, 17, 0, 0, 0, time.UTC)
	timing := ScheduleTiming{Timezone: "America/Los_Angeles", SkipDates: []string{"2026-09-28"}, Rules: []ScheduleRule{
		{Frequency: "weekly", Interval: 1, StartDate: "2026-09-01", Weekdays: []int{1}, Times: []string{"09:00"}},
		{Frequency: "monthly", Interval: 1, StartDate: "2026-09-01", MonthDays: []int{1}, Times: []string{"08:00"}},
	}}
	next, ok, err := timing.NextRun(now)
	location, _ := time.LoadLocation("America/Los_Angeles")
	if err != nil || !ok || next.In(location).Format("2006-01-02T15:04") != "2026-10-01T08:00" {
		t.Fatalf("next = %v %v %v", next.In(location), ok, err)
	}
	ended := ScheduleTiming{Timezone: "UTC", Rules: []ScheduleRule{{Frequency: "daily", Interval: 1, StartDate: "2026-09-01", EndDate: "2026-09-20", Times: []string{"09:00"}}}}
	if _, ok, err := ended.NextRun(now); err != nil || ok {
		t.Fatalf("an ended rule produced a run: %v %v", ok, err)
	}
	past := ScheduleTiming{Timezone: "UTC", Rules: []ScheduleRule{{Frequency: "once", Dates: []string{"2026-09-01"}, Times: []string{"09:00"}}}}
	if _, ok, err := past.NextRun(now); err != nil || ok {
		t.Fatalf("a past date produced a run: %v %v", ok, err)
	}
}

func TestScheduleTimingNormalizesAndRejectsBadInput(t *testing.T) {
	timing := ScheduleTiming{Timezone: "UTC", Rules: []ScheduleRule{{Frequency: "daily", Times: []string{"09:00"}}}}
	timing.Normalize(time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC))
	if timing.Rules[0].Interval != 1 || timing.Rules[0].StartDate != "2026-09-25" || timing.Validate() != nil {
		t.Fatalf("normalized = %+v", timing.Rules[0])
	}
	for _, bad := range []ScheduleTiming{
		{Timezone: "UTC"},
		{Timezone: "local", Rules: []ScheduleRule{{Frequency: "daily", Interval: 1, StartDate: "2026-09-01", Times: []string{"09:00"}}}},
		{Timezone: "UTC", Rules: []ScheduleRule{{Frequency: "minutely", Interval: 1, StartDate: "2026-09-01", Times: []string{"09:00"}}}},
		{Timezone: "UTC", Rules: []ScheduleRule{{Frequency: "daily", Interval: 1, StartDate: "2026-09-01", Times: []string{"25:00"}}}},
		{Timezone: "UTC", Rules: []ScheduleRule{{Frequency: "weekly", Interval: 1, StartDate: "2026-09-01", Times: []string{"09:00"}}}},
		{Timezone: "UTC", Rules: []ScheduleRule{{Frequency: "monthly", Interval: 1, StartDate: "2026-09-01", Times: []string{"09:00"}}}},
		{Timezone: "UTC", Rules: []ScheduleRule{{Frequency: "yearly", Interval: 1, StartDate: "2026-09-01", MonthDays: []int{1}, Times: []string{"09:00"}}}},
		{Timezone: "UTC", Rules: []ScheduleRule{{Frequency: "once", Times: []string{"09:00"}}}},
		{Timezone: "UTC", Rules: []ScheduleRule{{Frequency: "daily", Interval: 1, StartDate: "2026-09-10", EndDate: "2026-09-01", Times: []string{"09:00"}}}},
	} {
		if bad.Validate() == nil {
			t.Fatalf("expected %+v to be rejected", bad)
		}
	}
}
