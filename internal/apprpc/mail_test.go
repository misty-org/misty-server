package apprpc

import (
	"encoding/json"
	"github.com/go-chi/chi/v5"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestMailProviderIDsAreNarrowlyAllowedAndEncodedOnce(t *testing.T) {
	for _, value := range []string{"a+b/c==", "%2F", "%25?x#y", "a/../b", strings.Repeat("x", 320)} {
		request := Request{Protocol: 2, Method: "mail.threads.get", Params: Params{Path: map[string]string{"threadID": value}}}
		target, err := Resolve(request, "space_1")
		if err != nil || target.Path != "/mail/threads/"+url.PathEscape(value) {
			t.Fatalf("resolve %q: %#v %v", value, target, err)
		}
	}
	for _, value := range []string{"", ".", "..", "a b", "a\n", "雪", strings.Repeat("x", 321)} {
		if _, err := Resolve(Request{Protocol: 2, Method: "mail.threads.get", Params: Params{Path: map[string]string{"threadID": value}}}, "space_1"); err == nil {
			t.Fatalf("accepted invalid provider ID %q", value)
		}
	}
	if _, err := Resolve(Request{Protocol: 2, Method: "notes.get", Params: Params{Path: map[string]string{"noteID": "a+b=="}}}, "space_1"); err == nil {
		t.Fatal("relaxed unrelated identifier")
	}
}

func TestMailEnvelopeLimitsAndSendConsentPrecedeDispatch(t *testing.T) {
	for _, item := range []struct {
		name, method string
		scopes       []string
		body         string
		padding      int
		want         int
	}{
		{"large draft", "mail.drafts.create", []string{"mail.write"}, `{"text":"` + strings.Repeat("x", 5<<20) + `"}`, 0, 204},
		{"large draft without write", "mail.drafts.create", []string{"mail.read"}, `{"text":"` + strings.Repeat("x", 5<<20) + `"}`, 0, 400},
		{"other method remains small", "notes.create", []string{"mail.write"}, `{"title":"` + strings.Repeat("x", 5<<20) + `"}`, 0, 400},
		{"large ordinary whitespace", "notes.list", []string{"mail.write"}, "null", 5 << 20, 400},
		{"oversized draft", "mail.drafts.create", []string{"mail.write"}, `{"text":"` + strings.Repeat("x", mailJSONLimit) + `"}`, 0, 400},
		{"unconfirmed send", "mail.drafts.send", []string{"mail.write"}, `{"connection_id":"connection_1","authoring_source":"user","confirmed":false}`, 0, 400},
		{"missing source", "mail.drafts.send", []string{"mail.write"}, `{"connection_id":"connection_1","confirmed":true}`, 0, 400},
		{"confirmed send", "mail.drafts.send", []string{"mail.write"}, `{"connection_id":"connection_1","authoring_source":"ai","confirmed":true}`, 0, 204},
	} {
		t.Run(item.name, func(t *testing.T) {
			called := false
			handler := Handler{Authenticate: func(http.ResponseWriter, *http.Request) (Identity, bool) {
				return Identity{SpaceID: "space_1", Scopes: item.scopes}, true
			}, Authorize: func(Identity, string, string) bool { return true },
				Dispatch: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true; w.WriteHeader(204) })}
			params := Params{Body: json.RawMessage(item.body)}
			if item.method == "mail.drafts.send" {
				params.Path = map[string]string{"draftID": "draft_1"}
			}
			data, err := json.Marshal(Request{Protocol: 2, Method: item.method, Params: params})
			if err != nil {
				t.Fatal(err)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest("POST", "/rpc", strings.NewReader(strings.Repeat(" ", item.padding)+string(data))))
			if response.Code != item.want || called != (item.want == 204) {
				t.Fatalf("status=%d called=%v body=%s", response.Code, called, response.Body.String())
			}
		})
	}
}

func TestMailForwardedURLPreservesEncodedSegmentsForChi(t *testing.T) {
	router := chi.NewRouter()
	var expected string
	router.Get("/api/mail/threads/{threadID}", func(w http.ResponseWriter, r *http.Request) {
		value, err := url.PathUnescape(chi.URLParam(r, "threadID"))
		if err != nil || value != expected {
			t.Fatalf("received %q instead of %q (%v)", value, expected, err)
		}
		if r.URL.EscapedPath() != r.URL.RawPath {
			t.Fatal("inconsistent URL encoding")
		}
		w.WriteHeader(204)
	})
	handler := Handler{Prefix: "/api", Dispatch: router, Authenticate: func(http.ResponseWriter, *http.Request) (Identity, bool) { return Identity{SpaceID: "space_1"}, true }, Authorize: func(Identity, string, string) bool { return true }}
	for _, expected = range []string{"a+b/c==", "%2F", "%25?x#y", "a/../b"} {
		data, _ := json.Marshal(Request{Protocol: 2, Method: "mail.threads.get", Params: Params{Path: map[string]string{"threadID": expected}}})
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest("POST", "/rpc", strings.NewReader(string(data))))
		if response.Code != 204 {
			t.Fatalf("route failed: %d %s", response.Code, response.Body.String())
		}
	}
}
