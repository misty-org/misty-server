package api

import (
	"bytes"
	"encoding/json"
	"strconv"
	"time"

	"github.com/misty-org/misty-server/internal/platform/db"
)

type operatorInvalid string

func (e operatorInvalid) Error() string { return string(e) }

type operatorLimits struct {
	fields map[string]json.RawMessage
}

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
		"shared_spaces": &base.SharedSpaces, "space_members": &base.SpaceMembers,
		"spaces": &base.Spaces, "upload_bytes": &base.UploadBytes,
		"friend_requests_per_day": &base.FriendRequestsPerDay,
		"integrations_per_space":  &base.IntegrationsPerSpace,
	}
	for name, raw := range l.fields {
		target, known := targets[name]
		if retiredLimits[name] {
			continue
		}
		if !known {
			return base, operatorInvalid("unknown limit " + strconv.Quote(name) + "; limits are storage_bytes, devices, shared_spaces, space_members, spaces, upload_bytes, friend_requests_per_day and integrations_per_space")
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
