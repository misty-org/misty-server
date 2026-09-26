package api

import "net/http"

type sdkFixtureTransport func(*http.Request) (*http.Response, error)

func (f sdkFixtureTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
