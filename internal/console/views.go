package console

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/a-h/templ"
)

type layoutView struct {
	Title   string
	Active  string
	Nav     []NavGroup
	CSRF    string
	Content templ.Component
}

// csrfHeaders is the hx-headers value that makes every htmx request carry the
// session's CSRF token.
func (v layoutView) csrfHeaders() string {
	data, _ := json.Marshal(map[string]string{"X-CSRF-Token": v.CSRF})
	return string(data)
}

// ago formats a past time as "2 min ago", "3 h ago" or a date.
func ago(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d h ago", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%d d ago", int(d.Hours())/24)
	default:
		return t.Local().Format("Jan 2, 2006")
	}
}

func agoPtr(t *time.Time) string {
	if t == nil {
		return "never"
	}
	return ago(*t)
}

func date(t time.Time) string {
	return t.Local().Format("Jan 2, 2006")
}

// bytesLabel formats a byte count as "2.1 GB".
func bytesLabel(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

func itoa(n int) string { return fmt.Sprint(n) }

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// humanize turns identifiers such as awaiting_approval into "Awaiting approval".
func humanize(value string) string {
	value = strings.ReplaceAll(value, "_", " ")
	if value == "" {
		return value
	}
	return strings.ToUpper(value[:1]) + value[1:]
}

// problemState reports whether a job or record state deserves emphasis.
func problemState(state string) bool {
	switch state {
	case "failed", "partially_failed", "degraded", "unavailable", "exited", "dead", "restarting", "unhealthy":
		return true
	}
	return false
}
