package api

import (
	"github.com/go-chi/chi/v5"
	"net/http"
	"net/url"
)

// Chi matches RawPath when present, otherwise the already-decoded Path.
// Decode exactly once; QueryUnescape would corrupt literal plus characters.
func mailPathID(r *http.Request, name string) string {
	value := chi.URLParam(r, name)
	if r.URL.RawPath != "" {
		decoded, err := url.PathUnescape(value)
		if err != nil {
			return ""
		}
		return decoded
	}
	return value
}
