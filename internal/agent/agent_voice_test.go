package agent

import (
	"net/http"
)

type voiceTransport func(*http.Request) (*http.Response, error)

func (fn voiceTransport) RoundTrip(r *http.Request) (*http.Response, error) { return fn(r) }
