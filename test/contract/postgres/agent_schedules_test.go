package db

import (
	"testing"
	"time"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestAgentScheduleDueCoalescesMissedIntervals(t *testing.T) {
	baseline := time.Date(2026, 7, 14, 8, 0, 0, 0, time.UTC)
	if !TestingAgentScheduleDue("0 9 * * 1-5", baseline, time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)) {
		t.Fatal("missed weekday schedule was not due")
	}
	if TestingAgentScheduleDue("0 9 * * 1-5", baseline, time.Date(2026, 7, 14, 8, 30, 0, 0, time.UTC)) {
		t.Fatal("future weekday schedule was due early")
	}
	if TestingAgentScheduleDue("not cron", baseline, time.Now()) {
		t.Fatal("invalid cron expression was accepted")
	}
}

func TestNextAgentScheduleUsesUserTimezoneAcrossDST(t *testing.T) {
	location, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	// 02:30 does not exist on the spring-forward day; it must not be
	// fabricated as a UTC/local conversion artifact.
	baseline := time.Date(2026, 3, 8, 0, 0, 0, 0, location)
	if _, due := TestingNextAgentSchedule("30 2 * * *", "America/Los_Angeles", baseline, time.Date(2026, 3, 8, 4, 0, 0, 0, location)); due {
		t.Fatal("nonexistent spring-forward wall time was scheduled")
	}
	// The first 01:30 occurrence on fall-back day is a real due occurrence;
	// event IDs deduplicate any repeated coordinator observation.
	baseline = time.Date(2026, 11, 1, 0, 0, 0, 0, location)
	if _, due := TestingNextAgentSchedule("30 1 * * *", "America/Los_Angeles", baseline, time.Date(2026, 11, 1, 1, 45, 0, 0, location)); !due {
		t.Fatal("fall-back schedule was not due")
	}
	if _, due := TestingNextAgentSchedule("0 9 * * *", "local", baseline, time.Now()); due {
		t.Fatal("ambiguous local timezone was accepted")
	}
}
