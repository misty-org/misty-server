package api

import (
	"encoding/json"
	"errors"
	"github.com/misty-org/misty-server/internal/platform/transport"
	"io"
	"net/http"
)

const maxJSONBodyBytes = 8 << 10

var errInvalidJSON = errors.New("invalid json request body")

// decodeJSON reads exactly one JSON object from the request body into dst.
// It writes the 400 response itself, so the usual caller shape
// `if decodeJSON(w, r, &body) != nil { return }` cannot fall through to an
// empty 200, which a client would read as the change having been applied.
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
	transport.WriteJSON(w, status, payload)
}
