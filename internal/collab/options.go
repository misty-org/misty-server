package collab

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
)

const (
	defaultTextValue = "text"
	maxOptionBytes   = 4096
	maxFileIDBytes   = 128
	maxTextNameBytes = 256
)

var ErrBadOptions = errors.New("collab: the options could not be read")

type Options struct {
	File    string
	Text    string
	History bool
	GC      bool
}

type optionsJSON struct {
	File    *string `json:"file,omitempty"`
	Text    *string `json:"text,omitempty"`
	History bool    `json:"history"`
	GC      bool    `json:"gc"`
}

func (o Options) JSON() json.RawMessage {
	out := map[string]any{"history": o.History, "gc": o.GC}
	if o.File != "" {
		out["file"] = o.File
		if o.Text == "" {
			out["text"] = nil
		} else {
			out["text"] = o.Text
		}
	}
	raw, _ := json.Marshal(out)
	return raw
}

func (o Options) WholeDocument() bool { return o.File != "" && o.Text == "" }

func ParseStoredOptions(raw []byte) Options {
	var stored optionsJSON
	if json.Unmarshal(raw, &stored) != nil {
		return Options{GC: true}
	}
	options := Options{History: stored.History, GC: stored.GC}
	if stored.File != nil {
		options.File = *stored.File
		if stored.Text != nil {
			options.Text = *stored.Text
		}
	}
	return options
}

type Requested struct {
	File      *string
	Text      *string
	TextGiven bool
	History   *bool
	GC        *bool
}

func ParseRequested(raw json.RawMessage) (Requested, error) {
	var requested Requested
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return requested, nil
	}
	if len(trimmed) > maxOptionBytes {
		return Requested{}, ErrBadOptions
	}
	fields := map[string]json.RawMessage{}
	if json.Unmarshal(trimmed, &fields) != nil {
		return Requested{}, ErrBadOptions
	}
	for name, value := range fields {
		switch name {
		case "file":
			var file string
			if json.Unmarshal(value, &file) != nil {
				return Requested{}, ErrBadOptions
			}
			file = strings.TrimSpace(file)
			if file == "" || len(file) > maxFileIDBytes {
				return Requested{}, ErrBadOptions
			}
			requested.File = &file
		case "text":
			requested.TextGiven = true
			if string(bytes.TrimSpace(value)) == "null" {
				continue
			}
			var text string
			if json.Unmarshal(value, &text) != nil || text == "" || len(text) > maxTextNameBytes {
				return Requested{}, ErrBadOptions
			}
			requested.Text = &text
		case "history":
			var history bool
			if json.Unmarshal(value, &history) != nil {
				return Requested{}, ErrBadOptions
			}
			requested.History = &history
		case "gc":
			var gc bool
			if json.Unmarshal(value, &gc) != nil {
				return Requested{}, ErrBadOptions
			}
			requested.GC = &gc
		}
	}
	return requested, nil
}

func (r Requested) New() Options {
	options := Options{GC: true}
	if r.File != nil {
		options.File = *r.File
		options.Text = defaultTextValue
		if r.TextGiven {
			options.Text = ""
			if r.Text != nil {
				options.Text = *r.Text
			}
		}
	}
	if r.History != nil {
		options.History = *r.History
	}
	if r.GC != nil {
		options.GC = *r.GC
	}
	return options
}

func (r Requested) Fits(o Options) bool {
	if r.File != nil && *r.File != o.File {
		return false
	}
	if r.TextGiven && o.File != "" {
		want := ""
		if r.Text != nil {
			want = *r.Text
		}
		if want != o.Text {
			return false
		}
	}
	if r.History != nil && *r.History != o.History {
		return false
	}
	if r.GC != nil && *r.GC != o.GC {
		return false
	}
	return true
}

func (r Requested) FileID() string {
	if r.File == nil {
		return ""
	}
	return *r.File
}
