package capabilities

import (
	"encoding/json"
	"time"
)

type Invocation struct {
	RequestID         string          `json:"requestId"`
	Capability        string          `json:"capability"`
	CapabilityVersion int             `json:"capabilityVersion"`
	ProviderID        string          `json:"providerId"`
	ProviderVersion   int             `json:"providerVersion"`
	TargetID          string          `json:"targetId"`
	TargetRevision    int             `json:"targetRevision"`
	Input             json.RawMessage `json:"input"`
	Deadline          time.Time       `json:"deadline"`
}

func (i Invocation) Validate(now time.Time) error {
	if !ValidID(i.RequestID) || !ValidName(i.Capability) || !validVersion(i.CapabilityVersion) || !ValidProviderID(i.ProviderID) || !validVersion(i.ProviderVersion) || !ValidID(i.TargetID) || !validVersion(i.TargetRevision) || len(i.Input) == 0 || len(i.Input) > 512<<10 || !json.Valid(i.Input) || !i.Deadline.After(now) || i.Deadline.After(now.Add(24*time.Hour)) {
		return ErrInvalid
	}
	return nil
}

type Execution struct {
	Invocation
	RunID    string   `json:"runId"`
	EffectID string   `json:"effectId"`
	GrantIDs []string `json:"grantIds"`
}
type Evidence struct {
	TargetID   string    `json:"targetId"`
	ObservedAt time.Time `json:"observedAt"`
	Kind       string    `json:"kind"`
	Reference  string    `json:"reference"`
	Revision   string    `json:"revision,omitempty"`
	Excerpt    string    `json:"excerpt,omitempty"`
}

// Backend adapters report observations. They cannot mint approval or wait IDs;
// only the host can turn authentication/availability failures into durable waits.
type BackendOutcome struct {
	Status    string          `json:"status"`
	Result    json.RawMessage `json:"result,omitempty"`
	Evidence  []Evidence      `json:"evidence,omitempty"`
	Partial   bool            `json:"partial,omitempty"`
	Code      string          `json:"code,omitempty"`
	Message   string          `json:"message,omitempty"`
	Retryable bool            `json:"retryable,omitempty"`
	EffectID  string          `json:"effectId,omitempty"`
	Reason    string          `json:"reason,omitempty"`
}

func ParseBackendOutcome(raw []byte, request Invocation, effectID string, definition Definition, now time.Time) (*BackendOutcome, error) {
	var outcome BackendOutcome
	if len(raw) > 1<<20 || Decode(raw, &outcome) != nil {
		return nil, ErrInvalid
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return nil, ErrInvalid
	}
	allowed := map[string]bool{"status": true}
	switch outcome.Status {
	case "success":
		for _, key := range []string{"result", "evidence", "partial"} {
			if _, ok := fields[key]; !ok {
				return nil, ErrInvalid
			}
			allowed[key] = true
		}
		if string(fields["partial"]) != "true" && string(fields["partial"]) != "false" || len(fields["result"]) > 512<<10 || outcome.Evidence == nil {
			return nil, ErrInvalid
		}
		schema, err := CompileSchema(definition.OutputSchema)
		if err != nil {
			return nil, err
		}
		var value any
		if json.Unmarshal(outcome.Result, &value) != nil || schema.Validate(value) != nil {
			return nil, ErrInvalid
		}
	case "failure":
		for _, key := range []string{"code", "message", "retryable"} {
			if _, ok := fields[key]; !ok {
				return nil, ErrInvalid
			}
			allowed[key] = true
		}
		if !textWithin(outcome.Code, 1, 100) || !textWithin(outcome.Message, 1, 2000) || (string(fields["retryable"]) != "true" && string(fields["retryable"]) != "false") {
			return nil, ErrInvalid
		}
	case "uncertain":
		for _, key := range []string{"effectId", "reason", "evidence"} {
			if _, ok := fields[key]; !ok {
				return nil, ErrInvalid
			}
			allowed[key] = true
		}
		if outcome.EffectID != effectID || !textWithin(outcome.Reason, 1, 2000) || outcome.Evidence == nil {
			return nil, ErrInvalid
		}
	default:
		return nil, ErrInvalid
	}
	for key := range fields {
		if !allowed[key] {
			return nil, ErrInvalid
		}
	}
	if len(outcome.Evidence) > 100 {
		return nil, ErrInvalid
	}
	for _, e := range outcome.Evidence {
		if e.TargetID != request.TargetID || e.ObservedAt.IsZero() || e.ObservedAt.After(now.Add(time.Minute)) || !textWithin(e.Reference, 1, 2048) || !textWithin(e.Revision, 0, 200) || !textWithin(e.Excerpt, 0, 8000) || (e.Kind != "resource" && e.Kind != "browser" && e.Kind != "command" && e.Kind != "provider") {
			return nil, ErrInvalid
		}
	}
	return &outcome, nil
}
