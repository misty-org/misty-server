package workflow

import (
	"encoding/json"
	"errors"
)

var (
	ErrDeviceUnavailable  = errors.New("device_unavailable")
	ErrOutputInvalid      = errors.New("node_output_invalid")
	ErrUnsupportedContent = errors.New("unsupported_content_type")
	ErrAwaitingApproval   = errors.New("awaiting_approval")
)

type StepState string

const (
	StepRunning          StepState = "running"
	StepCooldown         StepState = "cooldown"
	StepCompleted        StepState = "completed"
	StepFailed           StepState = "failed"
)

type StepEvent struct {
	NodeID  string
	State   StepState
	Attempt int
	Input   json.RawMessage
	Output  json.RawMessage
	Error   error
}

