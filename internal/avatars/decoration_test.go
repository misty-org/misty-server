package avatars

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestParseDecoration(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		want    Decoration
		invalid bool
	}{
		{name: "valid color", body: `{"outline":"#1a2b3c"}`, want: Decoration{Outline: "#1a2b3c"}},
		{name: "uppercase is lowered", body: `{"outline":"#ABCDEF"}`, want: Decoration{Outline: "#abcdef"}},
		{name: "empty string removes", body: `{"outline":""}`, want: Decoration{}},
		{name: "null removes", body: `{"outline":null}`, want: Decoration{}},
		{name: "empty object", body: `{}`, want: Decoration{}},
		{name: "unknown key", body: `{"outline":"#1a2b3c","glow":"#ffffff"}`, invalid: true},
		{name: "short color", body: `{"outline":"#abc"}`, invalid: true},
		{name: "named color", body: `{"outline":"red"}`, invalid: true},
		{name: "no hash", body: `{"outline":"1a2b3c"}`, invalid: true},
		{name: "not hex", body: `{"outline":"#zzzzzz"}`, invalid: true},
		{name: "not a string", body: `{"outline":123}`, invalid: true},
		{name: "array", body: `[]`, invalid: true},
		{name: "null body", body: `null`, invalid: true},
		{name: "empty body", body: ``, invalid: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Parse([]byte(tc.body))
			if tc.invalid {
				if !errors.Is(err, ErrInvalidDecoration) {
					t.Fatalf("Parse(%s) = %+v, %v; want ErrInvalidDecoration", tc.body, got, err)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("Parse(%s) = %+v, %v; want %+v", tc.body, got, err, tc.want)
			}
		})
	}
}

func TestParseDecorationRejectsLargeBody(t *testing.T) {
	body := make([]byte, MaxDecorationBytes+1)
	for i := range body {
		body[i] = ' '
	}
	copy(body, `{"outline":"#1a2b3c"}`)
	if _, err := Parse(body); !errors.Is(err, ErrInvalidDecoration) {
		t.Fatalf("Parse(large) error = %v; want ErrInvalidDecoration", err)
	}
}

func TestDecorationJSONAndScan(t *testing.T) {
	empty, err := json.Marshal(Decoration{})
	if err != nil || string(empty) != `{}` {
		t.Fatalf("json.Marshal(Decoration{}) = %s, %v; want {}", empty, err)
	}
	var scanned Decoration
	if err := scanned.Scan([]byte(`{"outline":"#1a2b3c"}`)); err != nil || scanned.Outline != "#1a2b3c" {
		t.Fatalf("Scan() = %+v, %v", scanned, err)
	}
	if err := scanned.Scan(nil); err != nil || scanned != (Decoration{}) {
		t.Fatalf("Scan(nil) = %+v, %v; want empty", scanned, err)
	}
	value, err := Decoration{Outline: "#1a2b3c"}.Value()
	if err != nil || value != `{"outline":"#1a2b3c"}` {
		t.Fatalf("Value() = %v, %v", value, err)
	}
}
