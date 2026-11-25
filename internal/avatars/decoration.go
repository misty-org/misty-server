package avatars

import (
	"bytes"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

const MaxDecorationBytes = 1 << 10

var ErrInvalidDecoration = errors.New("invalid avatar decoration")

var outlinePattern = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

type Decoration struct {
	Outline string `json:"outline,omitempty"`
}

func Parse(data []byte) (Decoration, error) {
	var out Decoration
	if len(data) > MaxDecorationBytes {
		return out, fmt.Errorf("%w: body is larger than %d bytes", ErrInvalidDecoration, MaxDecorationBytes)
	}
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return out, fmt.Errorf("%w: must be a JSON object", ErrInvalidDecoration)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &fields); err != nil {
		return out, fmt.Errorf("%w: must be a JSON object", ErrInvalidDecoration)
	}
	for key, raw := range fields {
		switch key {
		case "outline":
			outline, err := parseOutline(raw)
			if err != nil {
				return Decoration{}, err
			}
			out.Outline = outline
		default:
			return Decoration{}, fmt.Errorf("%w: unknown key %q", ErrInvalidDecoration, key)
		}
	}
	return out, nil
}

func parseOutline(raw json.RawMessage) (string, error) {
	if string(bytes.TrimSpace(raw)) == "null" {
		return "", nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", fmt.Errorf("%w: outline must be a string", ErrInvalidDecoration)
	}
	if value == "" {
		return "", nil
	}
	if !outlinePattern.MatchString(value) {
		return "", fmt.Errorf("%w: outline must be a color like #1a2b3c", ErrInvalidDecoration)
	}
	return strings.ToLower(value), nil
}

func (d Decoration) Value() (driver.Value, error) {
	data, err := json.Marshal(d)
	if err != nil {
		return nil, err
	}
	return string(data), nil
}

func (d *Decoration) Scan(src any) error {
	*d = Decoration{}
	var data []byte
	switch value := src.(type) {
	case nil:
		return nil
	case []byte:
		data = value
	case string:
		data = []byte(value)
	default:
		return fmt.Errorf("avatar decoration: cannot scan %T", src)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	return json.Unmarshal(data, d)
}
