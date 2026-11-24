package wire

import (
	"encoding/json"
	"errors"
	"unicode/utf8"

	"github.com/reearth/ygo/encoding"
)

const (
	MessageSync           = 0
	MessageAwareness      = 1
	MessageAwarenessQuery = 3

	SyncStep1  = 0
	SyncStep2  = 1
	SyncUpdate = 2

	Version       = 1
	MaxKeyBytes   = 256
	MaxFrameBytes = 12 * 1024 * 1024
)

const (
	CloseNormal          = 1000
	CloseInternal        = 1011
	CloseBadOpen         = 4000
	CloseUnauthorized    = 4001
	CloseForbidden       = 4003
	CloseNotFound        = 4004
	CloseBadFrame        = 4005
	CloseOptionsConflict = 4006
	CloseTooLarge        = 4008
	CloseFull            = 4009
	CloseMoved           = 4010
	CloseGone            = 4011
	CloseAccessChanged   = 4012
	CloseSignedOut       = 4013
	CloseReauth          = 4014
)

var closeNames = map[int]string{
	CloseNormal: "normal", CloseInternal: "internal", CloseBadOpen: "bad_open", CloseUnauthorized: "unauthorized",
	CloseForbidden: "forbidden", CloseNotFound: "not_found", CloseBadFrame: "bad_frame",
	CloseOptionsConflict: "options_conflict", CloseTooLarge: "too_large", CloseFull: "full", CloseMoved: "moved",
	CloseGone: "gone", CloseAccessChanged: "access_changed", CloseSignedOut: "signed_out", CloseReauth: "reauth",
}

func CloseName(code int) string {
	if name, ok := closeNames[code]; ok {
		return name
	}
	return "internal"
}

var ErrBadFrame = errors.New("wire: the frame could not be read")

type Frame struct {
	Text bool
	Data []byte
}

func BinaryFrame(data []byte) Frame { return Frame{Data: data} }

func TextFrame(value any) Frame {
	raw, err := json.Marshal(value)
	if err != nil {
		raw = []byte(`{}`)
	}
	return Frame{Text: true, Data: raw}
}

type Open struct {
	Type    string          `json:"type"`
	V       int             `json:"v"`
	Space   string          `json:"space"`
	Key     string          `json:"key"`
	Options json.RawMessage `json:"options,omitempty"`
	Token   string          `json:"token,omitempty"`
	Client  string          `json:"client,omitempty"`
}

func ParseOpen(raw []byte) (Open, error) {
	var open Open
	if err := json.Unmarshal(raw, &open); err != nil {
		return Open{}, errors.New("the open message is not JSON")
	}
	if open.Type != "open" {
		return Open{}, errors.New("the first message must be open")
	}
	if open.V != Version {
		return Open{}, errors.New("unsupported protocol version")
	}
	if open.Space == "" || len(open.Space) > 128 {
		return Open{}, errors.New("space is required")
	}
	if !ValidKey(open.Key) {
		return Open{}, errors.New("key must be 1 to 256 bytes of UTF-8 with no control characters")
	}
	if len(open.Token) > 8192 || len(open.Client) > 256 {
		return Open{}, errors.New("the open message is too large")
	}
	return open, nil
}

func ValidKey(key string) bool {
	if len(key) < 1 || len(key) > MaxKeyBytes || !utf8.ValidString(key) {
		return false
	}
	for _, r := range key {
		if r < 0x20 || (r >= 0x7f && r < 0xa0) {
			return false
		}
	}
	return true
}

type Identity struct {
	Kind   string `json:"kind"`
	ID     string `json:"id"`
	Name   string `json:"name"`
	Avatar string `json:"avatar,omitempty"`
	Tone   string `json:"tone,omitempty"`
}

type Opened struct {
	Type     string          `json:"type"`
	Room     string          `json:"room"`
	Role     string          `json:"role"`
	Options  json.RawMessage `json:"options"`
	Seats    int             `json:"seats"`
	MaxBytes int             `json:"max_bytes"`
	Identity Identity        `json:"identity"`
	Conn     string          `json:"conn,omitempty"`
}

type ErrorMessage struct {
	Type    string `json:"type"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func Error(code, message string) Frame {
	return TextFrame(ErrorMessage{Type: "error", Code: code, Message: message})
}

func Moved(url string) Frame {
	return TextFrame(map[string]string{"type": "moved", "url": url})
}

func Kind(frame []byte) (uint64, []byte, error) {
	dec := encoding.NewDecoder(frame)
	kind, err := dec.ReadVarUint()
	if err != nil {
		return 0, nil, ErrBadFrame
	}
	return kind, dec.RemainingBytes(), nil
}

func Sync(payload []byte) (uint64, []byte, error) {
	dec := encoding.NewDecoder(payload)
	step, err := dec.ReadVarUint()
	if err != nil || step > SyncUpdate {
		return 0, nil, ErrBadFrame
	}
	data, err := dec.ReadVarBytes()
	if err != nil {
		return 0, nil, ErrBadFrame
	}
	return step, data, nil
}

func AwarenessPayload(payload []byte) ([]byte, error) {
	dec := encoding.NewDecoder(payload)
	data, err := dec.ReadVarBytes()
	if err != nil {
		return nil, ErrBadFrame
	}
	return data, nil
}

func syncFrame(step uint64, data []byte) []byte {
	enc := encoding.NewEncoder()
	enc.WriteVarUint(MessageSync)
	enc.WriteVarUint(step)
	enc.WriteVarBytes(data)
	return enc.Bytes()
}

func SyncStep1Frame(stateVector []byte) Frame { return BinaryFrame(syncFrame(SyncStep1, stateVector)) }

func SyncStep2Frame(update []byte) Frame { return BinaryFrame(syncFrame(SyncStep2, update)) }

func UpdateFrame(update []byte) Frame { return BinaryFrame(syncFrame(SyncUpdate, update)) }

func AwarenessFrame(update []byte) Frame {
	enc := encoding.NewEncoder()
	enc.WriteVarUint(MessageAwareness)
	enc.WriteVarBytes(update)
	return BinaryFrame(enc.Bytes())
}

type OptionMessage struct {
	Option  string `json:"option"`
	Type    string `json:"type"`
	Request *int64 `json:"request,omitempty"`
}

func ParseOption(raw []byte) (OptionMessage, map[string]json.RawMessage, bool) {
	fields := map[string]json.RawMessage{}
	if json.Unmarshal(raw, &fields) != nil {
		return OptionMessage{}, nil, false
	}
	var message OptionMessage
	if json.Unmarshal(raw, &message) != nil || message.Option == "" {
		return OptionMessage{}, fields, false
	}
	return message, fields, true
}
