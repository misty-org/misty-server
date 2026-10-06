package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type noteControlTransport func(*http.Request) (*http.Response, error)

func (f noteControlTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestNoteControlSignsResourceBinding(t *testing.T) {
	config := JournalCollabConfig{Host: "collab.invalid", controlSecret: []byte("test-control-secret"), roomSalt: []byte("test-room-salt")}
	service := &SpacesService{TestingJournalCollab: config}
	previous := TestingNoteControlHTTPClient
	t.Cleanup(func() { TestingNoteControlHTTPClient = previous })
	TestingNoteControlHTTPClient = &http.Client{Transport: noteControlTransport(func(r *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		var envelope TestingNoteControlEnvelope
		if err := json.Unmarshal(body, &envelope); err != nil {
			t.Fatal(err)
		}
		if envelope.ResourceID != "note-unopened" || envelope.Command != "bootstrap" {
			t.Fatalf("unexpected control envelope: %+v", envelope)
		}
		if r.URL.Path != "/parties/note-room/"+config.RoomID("note-unopened") {
			t.Fatal("incorrect room")
		}
		if r.Header.Get("X-Misty-Signature") != config.SignControlRequest(r.Header.Get("X-Misty-Timestamp"), body) {
			t.Fatal("resource binding is not signed")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"ok":true}`))}, nil
	})}
	if err := service.TestingDeliverNoteControlCommand(t.Context(), "note-unopened", "bootstrap", []byte(`{"title":"Research","markdown":"Verified"}`)); err != nil {
		t.Fatal(err)
	}
}
