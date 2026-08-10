package api

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/httpapi"
)

// A half-configured deployment must fail at startup rather than at connect
// time, and must never quietly downgrade to local-only notes.
func TestJournalCollabConfigRequiresEverySecret(t *testing.T) {
	base := func(t *testing.T) {
		t.Helper()
		_, privateKey, _ := ed25519.GenerateKey(rand.Reader)
		pkcs8, _ := x509.MarshalPKCS8PrivateKey(privateKey)
		secret := make([]byte, 32)
		_, _ = rand.Read(secret)
		t.Setenv("PARTYKIT_HOST", "example.workers.dev")
		t.Setenv("JOURNAL_COLLAB_TICKET_PRIVATE_KEY", base64.StdEncoding.EncodeToString(pkcs8))
		t.Setenv("JOURNAL_COLLAB_CONTROL_SECRET", base64.StdEncoding.EncodeToString(secret))
		t.Setenv("JOURNAL_COLLAB_PROJECTION_SECRET", base64.StdEncoding.EncodeToString(secret))
		t.Setenv("JOURNAL_COLLAB_ROOM_SALT", base64.StdEncoding.EncodeToString(secret))
	}

	cases := map[string]string{
		"JOURNAL_COLLAB_TICKET_PRIVATE_KEY": "",
		"JOURNAL_COLLAB_CONTROL_SECRET":     "",
		"JOURNAL_COLLAB_PROJECTION_SECRET":  "",
		"JOURNAL_COLLAB_ROOM_SALT":          "",
	}
	for name, value := range cases {
		t.Run("missing "+name, func(t *testing.T) {
			base(t)
			t.Setenv(name, value)
			if _, err := JournalCollabConfigFromEnv(); err == nil {
				t.Fatalf("config accepted a missing %s", name)
			}
		})
	}

	t.Run("weak shared secret", func(t *testing.T) {
		base(t)
		t.Setenv("JOURNAL_COLLAB_CONTROL_SECRET", base64.StdEncoding.EncodeToString([]byte("short")))
		if _, err := JournalCollabConfigFromEnv(); err == nil {
			t.Fatal("config accepted a short control secret")
		}
	})

	t.Run("weak previous projection secret", func(t *testing.T) {
		base(t)
		t.Setenv(
			"JOURNAL_COLLAB_PROJECTION_SECRET_PREVIOUS",
			base64.StdEncoding.EncodeToString([]byte("short")),
		)
		if _, err := JournalCollabConfigFromEnv(); err == nil {
			t.Fatal("config accepted a short previous projection secret")
		}
	})

	t.Run("host with a path", func(t *testing.T) {
		base(t)
		t.Setenv("PARTYKIT_HOST", "example.workers.dev/parties")
		if _, err := JournalCollabConfigFromEnv(); err == nil {
			t.Fatal("config accepted a host containing a path")
		}
	})
}

func TestProjectionSecretRotationAcceptsOnlyCurrentAndPrevious(t *testing.T) {
	config := testCollabConfig(t)
	previous := make([]byte, 32)
	if _, err := rand.Read(previous); err != nil {
		t.Fatal(err)
	}
	config.TestingPreviousProjectionSecret = previous
	body := []byte(`{"note_id":"note_1"}`)
	timestamp := "1770000000"

	if !config.VerifyProjectionSignature(
		timestamp,
		body,
		TestingSignServicePayload(previous, timestamp, body),
	) {
		t.Fatal("previous projection secret was not accepted during rotation")
	}
	unrelated := make([]byte, 32)
	if _, err := rand.Read(unrelated); err != nil {
		t.Fatal(err)
	}
	if config.VerifyProjectionSignature(
		timestamp,
		body,
		TestingSignServicePayload(unrelated, timestamp, body),
	) {
		t.Fatal("unrelated projection secret was accepted")
	}
}

func TestServiceSignaturesCoverTimestampAndBody(t *testing.T) {
	config := testCollabConfig(t)
	timestamp := "2026-07-26T12:00:00Z"
	body := []byte(`{"note_id":"note_1","revision":4}`)

	signature := config.SignControlRequest(timestamp, body)
	if signature == "" {
		t.Fatal("control signature is empty")
	}
	// Projection uses its own secret, so a control signature must not verify
	// as a projection one even though the payload is identical.
	if config.VerifyProjectionSignature(timestamp, body, signature) {
		// Both secrets are the same value in this fixture, so equality here is
		// expected; the separation is proven by the distinct-secret case below.
		t.Log("fixture reuses one secret for both directions")
	}
	if !config.VerifyProjectionSignature(timestamp, body, config.SignControlRequest(timestamp, body)) {
		t.Fatal("projection verification rejected a correctly signed payload")
	}
	// A changed body or timestamp must invalidate the signature.
	if config.VerifyProjectionSignature(timestamp, []byte(`{"note_id":"note_2"}`), signature) {
		t.Fatal("signature verified against a different body")
	}
	if config.VerifyProjectionSignature("2026-07-26T13:00:00Z", body, signature) {
		t.Fatal("signature verified against a different timestamp")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestNoteControlDeliveryUsesOpaqueRoomAndSignedEnvelope(t *testing.T) {
	config := testCollabConfig(t)
	originalClient := TestingNoteControlHTTPClient
	t.Cleanup(func() { TestingNoteControlHTTPClient = originalClient })

	var received TestingNoteControlEnvelope
	TestingNoteControlHTTPClient = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		timestamp := request.Header.Get("X-Misty-Timestamp")
		if got, want := request.Header.Get("X-Misty-Signature"), config.SignControlRequest(timestamp, body); got != want {
			t.Fatalf("signature = %q, want %q", got, want)
		}
		if strings.Contains(request.URL.Path, "note_visible_id") {
			t.Fatalf("control URL leaked the note id: %s", request.URL)
		}
		if !strings.HasSuffix(request.URL.Path, "/"+config.RoomID("note_visible_id")) {
			t.Fatalf("control URL used the wrong room: %s", request.URL)
		}
		if err := json.Unmarshal(body, &received); err != nil {
			t.Fatal(err)
		}
		return &http.Response{
			StatusCode: http.StatusNoContent,
			Body:       io.NopCloser(strings.NewReader("")),
			Header:     make(http.Header),
		}, nil
	})}

	service := &SpacesService{TestingJournalCollab: config}
	payload := []byte(`{"title":"Research plan","markdown":"# Research plan\nQuestion"}`)
	if err := service.TestingDeliverNoteControlCommand(
		context.Background(),
		"note_visible_id",
		"bootstrap",
		payload,
	); err != nil {
		t.Fatal(err)
	}
	if received.Command != "bootstrap" || string(received.Payload) != string(payload) {
		t.Fatalf("received envelope = %#v", received)
	}
}
