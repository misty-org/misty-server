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

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	return decodeJSONUpTo(w, r, dst, maxJSONBodyBytes)
}

func decodeJSONUpTo(w http.ResponseWriter, r *http.Request, dst any, limit int64) error {
	r.Body = http.MaxBytesReader(w, r.Body, limit)

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
