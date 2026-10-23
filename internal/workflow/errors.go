package workflow

import (
	"encoding/json"
	"errors"
)

var (
	ErrCapabilityDenied   = errors.New("workflow capability denied")
	ErrDeviceUnavailable  = errors.New("device_unavailable")
	ErrOutputInvalid      = errors.New("node_output_invalid")
	ErrUnsupportedContent = errors.New("unsupported_content_type")
)

type StepState string

const (
	StepRunning   StepState = "running"
	StepCompleted StepState = "completed"
	StepFailed    StepState = "failed"
)

type StepEvent struct {
	NodeID  string
	State   StepState
	Attempt int
	Input   json.RawMessage
	Output  json.RawMessage
	Error   error
}
