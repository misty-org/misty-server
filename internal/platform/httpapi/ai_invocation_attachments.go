package api

import "encoding/json"

// What a request to Misty carries beside its prompt: a selection, captures of
// the screen, and the device contexts it may use.

type aiSelectionSnapshot struct {
	Kind        string         `json:"kind"`
	Content     string         `json:"content,omitempty"`
	Object      map[string]any `json:"object"`
	Anchors     map[string]any `json:"anchors,omitempty"`
	ContentHash string         `json:"contentHash"`
}

type aiCaptureAttachment struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	MimeType    string `json:"mime_type"`
	DataURL     string `json:"data_url"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	ContentHash string `json:"content_hash"`
}

type aiDisplayCapture struct {
	aiCaptureAttachment
	CapturedAt int64  `json:"captured_at,omitempty"`
	DisplayID  uint32 `json:"display_id,omitempty"`
	Source     string `json:"source,omitempty"`
	Screen     string `json:"screen"`
	Primary    bool   `json:"primary"`
	// Controls are Misty's own visible controls on this screen, read from its
	// interface, in this capture's pixels. Labels may include page titles.
	Controls []aiScreenControl `json:"controls,omitempty"`
}

// aiScreenControl is one named Misty control; X and Y are its center.
type aiScreenControl struct {
	Label  string `json:"label"`
	Role   string `json:"role"`
	X      int    `json:"x"`
	Y      int    `json:"y"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	// Reveal is "hover" for a control that is invisible until the pointer is over it.
	Reveal string `json:"reveal,omitempty"`
}

type aiInvocationDeviceContext struct {
	DeviceID     string          `json:"device_id"`
	Kind         string          `json:"kind"`
	OpaqueRef    string          `json:"opaque_ref"`
	DisplayName  string          `json:"display_name,omitempty"`
	Capabilities json.RawMessage `json:"capabilities"`
	Metadata     json.RawMessage `json:"metadata,omitempty"`
	// RunGrant is signed by the device that asked for this context.
	RunGrant *signedDeviceRecord `json:"run_grant,omitempty"`
}
