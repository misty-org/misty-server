package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestConditionalGETRevalidatesUnchangedSnapshots(t *testing.T) {
	body := `{"items":[1,2,3]}`
	calls := 0
	handler := ConditionalGET(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusCreated)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": []int{1, 2, 3}})
	})
	first := httptest.NewRecorder()
	handler(first, httptest.NewRequest(http.MethodGet, "/spaces", nil))
	etag := first.Header().Get("ETag")
	if first.Code != 200 || etag == "" || first.Header().Get("Cache-Control") != "private, no-cache" || first.Body.Len() < len(body) {
		t.Fatal("first read", first.Code, etag, first.Header(), first.Body.String())
	}
	again := httptest.NewRequest(http.MethodGet, "/spaces", nil)
	again.Header.Set("If-None-Match", etag)
	revalidated := httptest.NewRecorder()
	handler(revalidated, again)
	if revalidated.Code != http.StatusNotModified || revalidated.Body.Len() != 0 || calls != 2 {
		t.Fatal("unchanged snapshot must revalidate to an empty 304 and still run the handler", revalidated.Code, calls)
	}
	stale := httptest.NewRequest(http.MethodGet, "/spaces", nil)
	stale.Header.Set("If-None-Match", `W/"other"`)
	changed := httptest.NewRecorder()
	handler(changed, stale)
	if changed.Code != 200 || changed.Body.Len() == 0 {
		t.Fatal("changed snapshot must return its body", changed.Code)
	}
	post := httptest.NewRecorder()
	handler(post, httptest.NewRequest(http.MethodPost, "/spaces", nil))
	if post.Code != http.StatusCreated || post.Header().Get("ETag") != "" {
		t.Fatal("writes pass through")
	}
}

func TestConditionalGETLeavesErrorsAndNoStoreAlone(t *testing.T) {
	for _, handler := range []http.HandlerFunc{
		func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "nope", http.StatusForbidden) },
		func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			writeJSON(w, http.StatusOK, map[string]string{"secret": "x"})
		},
	} {
		recorder := httptest.NewRecorder()
		ConditionalGET(handler)(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
		if recorder.Header().Get("ETag") != "" {
			t.Fatal("tagged a response that must not be cached", recorder.Code)
		}
	}
}
