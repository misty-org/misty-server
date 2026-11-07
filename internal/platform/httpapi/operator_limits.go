package api

import (
	"bytes"
	"encoding/json"
	"strconv"
	"time"

	"github.com/misty-org/misty-server/internal/platform/db"
)

// operatorInvalid is a request the operator API refuses, with why.
type operatorInvalid string

func (e operatorInvalid) Error() string { return string(e) }

// operatorLimits is the `limits` object of the operator API. Each field it
// names is set: a non-negative integer, or null for unlimited. A field it
// leaves out keeps its current value (unlimited, for a new account).
type operatorLimits struct {
	fields map[string]json.RawMessage
}

// retiredLimits were monthly limits on web search and app actions. Both now run
// on the person's computer on their own keys, so the server counts neither; a
// billing service that still sends one has it ignored.
var retiredLimits = map[string]bool{"searches_per_month": true, "app_actions_per_month": true}

func (l *operatorLimits) UnmarshalJSON(data []byte) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return nil
	}
	return json.Unmarshal(data, &l.fields)
}

func (l operatorLimits) apply(base db.AccountLimits) (db.AccountLimits, error) {
	targets := map[string]**int64{
		"storage_bytes": &base.StorageBytes, "devices": &base.Devices,
	}
	for name, raw := range l.fields {
		target, known := targets[name]
		if retiredLimits[name] {
			continue
		}
		if !known {
			return base, operatorInvalid("unknown limit " + strconv.Quote(name) + "; limits are storage_bytes and devices")
		}
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			*target = nil
			continue
		}
		var value int64
		if err := json.Unmarshal(raw, &value); err != nil || value < 0 {
			return base, operatorInvalid(name + " must be a whole number of at least 0, or null for unlimited")
		}
		*target = &value
	}
	return base, nil
}

// parsePeriodAnchor reads PATCH's period_anchor: an RFC 3339 time sets it,
// null clears it (calendar months), and leaving it out keeps it.
func parsePeriodAnchor(raw json.RawMessage) (*time.Time, bool, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, false, nil
	}
	if bytes.Equal(trimmed, []byte("null")) {
		return nil, true, nil
	}
	var text string
	if err := json.Unmarshal(trimmed, &text); err == nil {
		if at, err := time.Parse(time.RFC3339, text); err == nil {
			return &at, true, nil
		}
	}
	return nil, false, operatorInvalid("period_anchor must be an RFC 3339 time, or null for calendar months")
}
