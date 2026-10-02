package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
)

// ConditionalGET lets a client revalidate a large snapshot instead of
// re-downloading it: a successful GET carries a content ETag and
// "private, no-cache", so the client's HTTP cache revalidates on every read and
// an unchanged snapshot answers 304 with no body. Each request still runs the
// handler, so authentication and authorization are unchanged, and another
// account's snapshot hashes differently. Other methods, errors and handlers
// that set no-store pass through untouched. Bodies are not compressed here.
func ConditionalGET(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			next(w, r)
			return
		}
		buffered := &snapshotRecorder{header: http.Header{}, status: http.StatusOK}
		next(buffered, r)
		for key, values := range buffered.header {
			w.Header()[key] = values
		}
		cacheable := buffered.status == http.StatusOK && !strings.Contains(buffered.header.Get("Cache-Control"), "no-store")
		if !cacheable {
			w.WriteHeader(buffered.status)
			_, _ = w.Write(buffered.body.Bytes())
			return
		}
		sum := sha256.Sum256(buffered.body.Bytes())
		etag := `W/"` + hex.EncodeToString(sum[:12]) + `"`
		w.Header().Set("ETag", etag)
		w.Header().Set("Cache-Control", "private, no-cache")
		w.Header().Add("Vary", "Cookie, Authorization")
		if etagMatches(r.Header.Get("If-None-Match"), etag) {
			w.Header().Del("Content-Length")
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(buffered.body.Bytes())
	}
}

func etagMatches(header, etag string) bool {
	for _, candidate := range strings.Split(header, ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "*" || strings.TrimPrefix(candidate, "W/") == strings.TrimPrefix(etag, "W/") {
			return true
		}
	}
	return false
}

type snapshotRecorder struct {
	header http.Header
	body   bytes.Buffer
	status int
	wrote  bool
}

func (r *snapshotRecorder) Header() http.Header { return r.header }
func (r *snapshotRecorder) WriteHeader(status int) {
	if !r.wrote {
		r.status, r.wrote = status, true
	}
}
func (r *snapshotRecorder) Write(p []byte) (int, error) {
	r.wrote = true
	return r.body.Write(p)
}
