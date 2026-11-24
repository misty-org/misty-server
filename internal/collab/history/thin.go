package history

import (
	"sort"
	"strconv"
	"time"
)

const (
	Cadence = 10 * time.Minute
	hour    = time.Hour
	day     = 24 * time.Hour
)

type Version struct {
	ID       string
	At       time.Time
	Size     int
	By       string
	Checksum string
}

func Thin(versions []Version, now time.Time, retentionDays int) []Version {
	sorted := append([]Version(nil), versions...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].At.Before(sorted[j].At) })
	latest := map[string]Version{}
	drop := []Version{}
	for _, version := range sorted {
		age := now.Sub(version.At)
		if retentionDays > 0 && age > time.Duration(retentionDays)*day {
			drop = append(drop, version)
			continue
		}
		bucket := ""
		millis := version.At.UnixMilli()
		switch {
		case age > 7*day:
			bucket = "day:" + strconv.FormatInt(floorDiv(millis, day.Milliseconds()), 10)
		case age > day:
			bucket = "hour:" + strconv.FormatInt(floorDiv(millis, hour.Milliseconds()), 10)
		}
		if bucket == "" {
			continue
		}
		if kept, ok := latest[bucket]; ok {
			drop = append(drop, kept)
		}
		latest[bucket] = version
	}
	return drop
}

func Kept(version Version, now time.Time, retentionDays int) bool {
	return retentionDays <= 0 || !version.At.Before(now.Add(-time.Duration(retentionDays)*day))
}

func floorDiv(a, b int64) int64 {
	q := a / b
	if (a%b != 0) && ((a < 0) != (b < 0)) {
		q--
	}
	return q
}
