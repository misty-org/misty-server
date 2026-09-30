package db

import (
	"testing"
	"time"
)

func TestScheduledTaskNextRun(t *testing.T) {
	// Friday 2026-09-25 10:00 in Los Angeles.
	now := time.Date(2026, 9, 25, 17, 0, 0, 0, time.UTC)
	la := "America/Los_Angeles"
	cases := []struct {
		name     string
		schedule ScheduledTaskSchedule
		want     string
		ok       bool
	}{
		{"daily later today", ScheduledTaskSchedule{Cadence: "daily", LocalTime: "11:30", MonthDay: 1, Timezone: la}, "2026-09-25T11:30", true},
		{"daily already passed", ScheduledTaskSchedule{Cadence: "daily", LocalTime: "09:00", MonthDay: 1, Timezone: la}, "2026-09-26T09:00", true},
		{"weekdays skip the weekend", ScheduledTaskSchedule{Cadence: "weekdays", LocalTime: "09:00", MonthDay: 1, Timezone: la}, "2026-09-28T09:00", true},
		{"weekly on Sunday", ScheduledTaskSchedule{Cadence: "weekly", LocalTime: "08:00", Weekday: 0, MonthDay: 1, Timezone: la}, "2026-09-27T08:00", true},
		{"monthly clamps to a short month", ScheduledTaskSchedule{Cadence: "monthly", LocalTime: "08:00", MonthDay: 31, Timezone: la}, "2026-09-30T08:00", true},
		{"once in the future", ScheduledTaskSchedule{Cadence: "once", LocalTime: "12:00", MonthDay: 1, RunOn: "2026-10-01", Timezone: la}, "2026-10-01T12:00", true},
		{"once in the past", ScheduledTaskSchedule{Cadence: "once", LocalTime: "12:00", MonthDay: 1, RunOn: "2026-09-01", Timezone: la}, "", false},
	}
	location, _ := time.LoadLocation(la)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			next, ok, err := tc.schedule.NextRun(now)
			if err != nil {
				t.Fatalf("NextRun: %v", err)
			}
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.ok)
			}
			if ok && next.In(location).Format("2006-01-02T15:04") != tc.want {
				t.Fatalf("next = %s, want %s", next.In(location).Format("2006-01-02T15:04"), tc.want)
			}
		})
	}
}

func TestScheduledTaskScheduleRejectsBadInput(t *testing.T) {
	for _, schedule := range []ScheduledTaskSchedule{
		{Cadence: "hourly", LocalTime: "09:00", MonthDay: 1, Timezone: "UTC"},
		{Cadence: "daily", LocalTime: "25:00", MonthDay: 1, Timezone: "UTC"},
		{Cadence: "daily", LocalTime: "09:00", MonthDay: 1, Timezone: "local"},
		{Cadence: "once", LocalTime: "09:00", MonthDay: 1, Timezone: "UTC"},
		{Cadence: "monthly", LocalTime: "09:00", MonthDay: 0, Timezone: "UTC"},
	} {
		if schedule.Validate() == nil {
			t.Fatalf("expected %+v to be rejected", schedule)
		}
	}
}
