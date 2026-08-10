package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

const maxJSONBodyBytes = 8 << 10

var errInvalidJSON = errors.New("invalid json request body")

// decodeJSON reads exactly one JSON object from the request body into dst.
//
// It writes the 400 response itself so that the overwhelmingly common caller
// shape — `if decodeJSON(w, r, &body) != nil { return }` — cannot fall through
// to an empty 200. A rejected request must never report success: callers that
// returned silently were answering unparseable bodies, unknown fields, and
// oversized payloads with a bare 200 and no body, which a client reads as the
// mutation having been applied.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBodyBytes)

	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(dst); err != nil {
		return rejectInvalidJSON(w)
	}

	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return rejectInvalidJSON(w)
	}

	return nil
}

func rejectInvalidJSON(w http.ResponseWriter) error {
	writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_request"})
	return errInvalidJSON
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
