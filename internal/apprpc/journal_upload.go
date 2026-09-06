package apprpc

import (
	"net/http"
	"strings"
)

// This is a named host transport credential, never arbitrary RPC-body headers.
func forwardJournalUploadCredential(destination, source http.Header, method string) error {
	if method != "notes.assets.finalize" && method != "drawings.assets.finalize" {
		return nil
	}
	const header = "X-Misty-Library-Upload-Token"
	values := source.Values(header)
	if len(values) == 0 {
		return nil // The domain handler remains responsible for missing credentials.
	}
	value := strings.TrimSpace(values[0])
	if len(values) != 1 || len(value) == 0 || len(value) > 1024 {
		return &Error{"invalid_request", "Invalid upload credential."}
	}
	for _, char := range value {
		if char < 0x21 || char > 0x7e || char == ',' {
			return &Error{"invalid_request", "Invalid upload credential."}
		}
	}
	destination.Set(header, value)
	return nil
}
