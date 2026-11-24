package history

import (
	"testing"
	"time"
)

func entry(id string, at time.Time) Version { return Version{ID: id, At: at, Size: 10, Checksum: id} }

func ids(versions []Version) map[string]bool {
	out := map[string]bool{}
	for _, version := range versions {
		out[version.ID] = true
	}
	return out
}

func TestKeepsEverythingFromTheLastDay(t *testing.T) {
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	versions := []Version{entry("a", now.Add(-30*time.Minute)), entry("b", now.Add(-20*time.Minute)), entry("c", now.Add(-10*time.Minute))}
	if drop := Thin(versions, now, 0); len(drop) != 0 {
		t.Fatalf("dropped %v", drop)
	}
}

func TestKeepsTheLatestOfEachHourAfterADayAndOfEachDayAfterAWeek(t *testing.T) {
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	hourStart := now.Add(-48 * time.Hour).Truncate(time.Hour)
	dayStart := now.Add(-10 * 24 * time.Hour).Truncate(24 * time.Hour)
	versions := []Version{
		entry("hour-early", hourStart.Add(5*time.Minute)),
		entry("hour-late", hourStart.Add(45*time.Minute)),
		entry("day-early", dayStart.Add(time.Hour)),
		entry("day-late", dayStart.Add(20*time.Hour)),
		entry("recent", now.Add(-time.Hour)),
	}
	dropped := ids(Thin(versions, now, 0))
	if len(dropped) != 2 || !dropped["day-early"] || !dropped["hour-early"] {
		t.Fatalf("dropped %v", dropped)
	}
}

func TestLetsGoOfVersionsPastTheHistoryWindow(t *testing.T) {
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	versions := []Version{entry("old", now.Add(-40*24*time.Hour)), entry("kept", now.Add(-2*time.Hour))}
	if dropped := ids(Thin(versions, now, 30)); len(dropped) != 1 || !dropped["old"] {
		t.Fatalf("dropped %v", dropped)
	}
	if dropped := Thin(versions, now, 0); len(dropped) != 0 {
		t.Fatalf("dropped %v without a window", dropped)
	}
	if Kept(versions[0], now, 30) || !Kept(versions[1], now, 30) || !Kept(versions[0], now, 0) {
		t.Fatal("Kept disagrees with the window")
	}
}
