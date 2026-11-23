package api

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const serviceCallbackBodyLimit = fileRoomTextBodyLimit

type trustedCallbackKey struct{}

func TrustedServiceCallback(r *http.Request) bool {
	trusted, _ := r.Context().Value(trustedCallbackKey{}).(bool)
	return trusted
}

func journalCallbackPath(path string) bool {
	return strings.HasPrefix(NormalizeRateLimitPath(path), "/internal/journal/")
}

func (c JournalCollabConfig) signedCallback(r *http.Request) bool {
	if !c.Enabled || r.Method != http.MethodPost || !journalCallbackPath(r.URL.Path) {
		return false
	}
	timestamp, signature := r.Header.Get("X-Misty-Timestamp"), r.Header.Get("X-Misty-Signature")
	issuedAt, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil || signature == "" || time.Since(time.Unix(issuedAt, 0)).Abs() > 5*time.Minute ||
		r.ContentLength > serviceCallbackBodyLimit || r.Body == nil {
		return false
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, serviceCallbackBodyLimit+1))
	r.Body = io.NopCloser(io.MultiReader(bytes.NewReader(body), r.Body))
	if err != nil || len(body) > serviceCallbackBodyLimit {
		return false
	}
	return c.VerifyProjectionSignature(timestamp, body, signature)
}

func TrustSignedServiceCallbacks(config func() JournalCollabConfig) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if config != nil && config().signedCallback(r) {
				r = r.WithContext(context.WithValue(r.Context(), trustedCallbackKey{}, true))
			}
			next.ServeHTTP(w, r)
		})
	}
}
