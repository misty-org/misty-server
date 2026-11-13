package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

const maxStrictJSONBodyBytes = 2 << 20

// decodeStrictJSON reads a request body that must be one JSON value of known fields.
func decodeStrictJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	return decodeStrictJSONWithLimit(w, r, dst, maxStrictJSONBodyBytes)
}

func decodeStrictJSONWithLimit(w http.ResponseWriter, r *http.Request, dst any, limit int64) error {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return errInvalidJSON
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errInvalidJSON
	}
	return nil
}

func mustRawJSON(value any) json.RawMessage { raw, _ := json.Marshal(value); return raw }

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// textExcerpt is the start of a text on one line, for a search result.
func textExcerpt(value string) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	const maximum = 240
	if len([]rune(value)) <= maximum {
		return value
	}
	return string([]rune(value)[:maximum]) + "…"
}
