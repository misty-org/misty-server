package app

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDrawingRoutesRequireAuthentication(t *testing.T) {
	server := noteRouteTestServer(t)
	cases := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/v1/spaces/space-1/drawings"},
		{http.MethodPost, "/v1/spaces/space-1/drawings"},
		{http.MethodGet, "/v1/spaces/space-1/drawings/drawing-1"},
		{http.MethodPatch, "/v1/spaces/space-1/drawings/drawing-1"},
		{http.MethodDelete, "/v1/spaces/space-1/drawings/drawing-1"},
		{
			http.MethodPost,
			"/v1/spaces/space-1/drawings/drawing-1/collaboration-ticket",
		},
	}

	for _, testCase := range cases {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(testCase.method, testCase.path, nil)
		server.Router.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf(
				"%s %s status = %d, want 401",
				testCase.method,
				testCase.path,
				recorder.Code,
			)
		}
	}
}

func TestDrawingRoutesAreMountedOnlyUnderV1(t *testing.T) {
	server := noteRouteTestServer(t)
	for _, path := range []string{"/spaces/space-1/drawings", "/api/spaces/space-1/drawings"} {
		recorder := httptest.NewRecorder()
		server.Router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("GET %s = %d, want 404", path, recorder.Code)
		}
	}
	for _, path := range []string{
		"/v1/spaces/space-1/drawings",
	} {
		recorder := httptest.NewRecorder()
		server.Router.ServeHTTP(
			recorder,
			httptest.NewRequest(http.MethodGet, path, nil),
		)
		if recorder.Code == http.StatusNotFound {
			t.Fatalf("GET %s returned 404", path)
		}
	}
}
