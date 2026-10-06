package db

import (
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

var standardAgentCronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

func TestingNextAgentSchedule(expression, timezone string, baseline, now time.Time) (time.Time, bool) {
	location, err := time.LoadLocation(strings.TrimSpace(timezone))
	if err != nil || strings.TrimSpace(timezone) == "" || timezone == "local" {
		return time.Time{}, false
	}
	spec, err := standardAgentCronParser.Parse(expression)
	if err != nil {
		return time.Time{}, false
	}
	next := spec.Next(baseline.In(location))
	return next, !next.After(now.In(location))
}

func TestingAgentScheduleDue(expression string, baseline, now time.Time) bool {
	spec, err := standardAgentCronParser.Parse(expression)
	return err == nil && !spec.Next(baseline).After(now)
}
