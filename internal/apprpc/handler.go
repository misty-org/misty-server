package apprpc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"

	"github.com/go-chi/chi/v5"
)

type Identity struct {
	AppID     string
	AccountID string
	SpaceID   string
	Scopes    []string
}
type Handler struct {
	Authenticate func(http.ResponseWriter, *http.Request) (Identity, bool)
	Authorize    func(Identity, string, string) bool
	Dispatch     http.Handler
	Prefix       string
}

func (handler Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	identity, ok := handler.Authenticate(w, r)
	if !ok {
		return
	}
	limit := int64(ordinaryEnvelopeLimit)
	if canWriteMail(identity) {
		limit = mailEnvelopeLimit
	}
	body := &countedReader{Reader: http.MaxBytesReader(w, r.Body, limit)}
	request, err := Decode(body)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := validateMailEnvelope(request, body.bytes, identity); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	target, err := Resolve(request, identity.SpaceID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if !handler.Authorize(identity, target.Verb, target.Path) {
		writeError(w, http.StatusForbidden, &Error{"app_scope_forbidden", "This App session does not grant that method."})
		return
	}
	ctx := context.WithValue(r.Context(), chi.RouteCtxKey, chi.NewRouteContext())
	forwarded := r.Clone(ctx)
	forwarded.Method = target.Verb
	forwarded.URL.RawPath = handler.Prefix + target.Path
	forwarded.URL.Path, err = url.PathUnescape(forwarded.URL.RawPath)
	if err != nil {
		writeError(w, http.StatusBadRequest, &Error{"invalid_params", "Invalid method path."})
		return
	}
	forwarded.URL.RawQuery = target.Query
	forwarded.RequestURI = forwarded.URL.RequestURI()
	forwarded.Body = ioBody(target.Body)
	forwarded.ContentLength = int64(len(target.Body))
	forwarded.GetBody = nil
	forwarded.Header = make(http.Header)
	forwarded.Header.Set("Authorization", r.Header.Get("Authorization"))
	forwarded.Header.Set("Content-Type", "application/json")
	if err := forwardJournalUploadCredential(forwarded.Header, r.Header, request.Method); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	for _, key := range []string{"Idempotency-Key", "If-Match", "If-None-Match", "X-Request-ID"} {
		if value := r.Header.Get(key); value != "" {
			forwarded.Header.Set(key, value)
		}
	}
	// Existing handlers re-check app-token permissions and domain membership.
	// Preserve their JSON/binary/stream response and cancellation behavior.
	handler.Dispatch.ServeHTTP(w, forwarded)
}

type bodyReader struct{ *bytes.Reader }

func (bodyReader) Close() error     { return nil }
func ioBody(body []byte) bodyReader { return bodyReader{bytes.NewReader(body)} }
func writeError(w http.ResponseWriter, status int, err error) {
	var typed *Error
	if !errors.As(err, &typed) {
		typed = &Error{"invalid_request", "Invalid SDK method request."}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"code": typed.Code, "message": typed.Message})
}
