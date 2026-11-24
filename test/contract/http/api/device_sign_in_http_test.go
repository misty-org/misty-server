package api

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/misty-org/misty-server/internal/platform/db"
	. "github.com/misty-org/misty-server/internal/platform/httpapi"
	"github.com/misty-org/misty-server/internal/platform/security"
	"github.com/misty-org/misty-server/internal/spaces"
)

func deviceSignInServer(database *db.Database) *httptest.Server {
	router := chi.NewRouter()
	router.Post("/v1/auth/device", StartDeviceSignIn(database))
	router.Get("/v1/auth/device/lookup", LookupDeviceSignIn(database))
	router.Post("/v1/auth/device/approve", ApproveDeviceSignIn(database))
	router.Post("/v1/auth/device/refuse", RefuseDeviceSignIn(database))
	router.Get("/v1/auth/device/{deviceID}", WaitDeviceSignIn(database))
	router.Post("/v1/auth/refresh", RefreshSession(database))
	router.Post("/v1/auth/integration", ExchangeIntegrationToken(database))
	router.Get("/v1/me/sessions", ListSessions(database))
	return httptest.NewServer(router)
}

func postJSON(t *testing.T, url, token, body string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

type deviceEvent struct {
	name string
	data map[string]any
}

func readDeviceEvent(body io.Reader) (deviceEvent, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64<<10), 64<<10)
	var event deviceEvent
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			event.name = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event.data); err != nil {
				return event, err
			}
		case line == "" && event.name != "":
			return event, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return event, err
	}
	return event, io.ErrUnexpectedEOF
}

func startDevice(t *testing.T, server *httptest.Server) (string, string) {
	t.Helper()
	response := postJSON(t, server.URL+"/v1/auth/device", "", `{"name":"misty-cli on studio-mac"}`)
	defer response.Body.Close()
	var started struct {
		ID              string `json:"id"`
		UserCode        string `json:"user_code"`
		VerificationURL string `json:"verification_url"`
		ExpiresIn       int    `json:"expires_in"`
	}
	if err := json.NewDecoder(response.Body).Decode(&started); err != nil || response.StatusCode != http.StatusCreated {
		t.Fatalf("POST /auth/device = %d, %v", response.StatusCode, err)
	}
	if started.ExpiresIn != 600 || started.VerificationURL != "misty://device?code="+started.UserCode || len(started.UserCode) != 9 {
		t.Fatalf("started = %#v", started)
	}
	return started.ID, started.UserCode
}

func waitForDevice(t *testing.T, server *httptest.Server, id string) <-chan deviceEvent {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/v1/auth/device/"+id, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || !strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("GET /auth/device/{id} = %d %s", response.StatusCode, response.Header.Get("Content-Type"))
	}
	events := make(chan deviceEvent, 1)
	go func() {
		defer response.Body.Close()
		event, err := readDeviceEvent(response.Body)
		if err != nil {
			event.name = "error: " + err.Error()
		}
		events <- event
	}()
	return events
}

func TestDeviceSignInDeliversASessionTheRefreshEndpointTakes(t *testing.T) {
	fixture := newBearerFixture(t)
	server := deviceSignInServer(fixture.database)
	defer server.Close()
	id, code := startDevice(t, server)
	events := waitForDevice(t, server, id)
	lookup, err := http.NewRequest(http.MethodGet, server.URL+"/v1/auth/device/lookup?code="+code, nil)
	if err != nil {
		t.Fatal(err)
	}
	lookup.Header.Set("Authorization", "Bearer "+fixture.access)
	looked, err := http.DefaultClient.Do(lookup)
	if err != nil || looked.StatusCode != http.StatusOK {
		t.Fatalf("lookup = %v, %v", looked, err)
	}
	looked.Body.Close()
	approved := postJSON(t, server.URL+"/v1/auth/device/approve", fixture.access, `{"user_code":"`+code+`"}`)
	approved.Body.Close()
	if approved.StatusCode != http.StatusNoContent {
		t.Fatalf("approve = %d", approved.StatusCode)
	}
	var event deviceEvent
	select {
	case event = <-events:
	case <-time.After(15 * time.Second):
		t.Fatal("the waiting device never heard of the approval")
	}
	if event.name != "approved" || len(event.data) != 4 {
		t.Fatalf("event = %#v", event)
	}
	refresh, _ := event.data["refresh_token"].(string)
	access, _ := event.data["access_token"].(string)
	sessionID, _ := event.data["session_id"].(string)
	if refresh == "" || access == "" || !strings.HasPrefix(sessionID, "ses_") || event.data["expires_in"] != float64(300) {
		t.Fatalf("approved data = %#v", event.data)
	}
	request, err := http.NewRequest(http.MethodPost, server.URL+"/v1/auth/refresh", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+refresh)
	request.Header.Set(TokenTransportHeader, "bearer")
	refreshed, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	refreshed.Body.Close()
	if refreshed.StatusCode != http.StatusNoContent || refreshed.Header.Get(AccessTokenHeader) == "" || refreshed.Header.Get(RefreshTokenHeader) == "" {
		t.Fatalf("refresh = %d, headers %v", refreshed.StatusCode, refreshed.Header)
	}
	sessions, err := fixture.database.AccountSessions(context.Background(), fixture.userID, "")
	if err != nil {
		t.Fatal(err)
	}
	named := false
	for _, session := range sessions {
		named = named || (session.ID == sessionID && session.Name == "misty-cli on studio-mac")
	}
	if !named {
		t.Fatalf("sessions = %#v, want one named after the device", sessions)
	}
}

func TestDeviceSignInRefusedEndsTheWait(t *testing.T) {
	fixture := newBearerFixture(t)
	server := deviceSignInServer(fixture.database)
	defer server.Close()
	id, code := startDevice(t, server)
	events := waitForDevice(t, server, id)
	refused := postJSON(t, server.URL+"/v1/auth/device/refuse", fixture.access, `{"user_code":"`+code+`"}`)
	refused.Body.Close()
	if refused.StatusCode != http.StatusNoContent {
		t.Fatalf("refuse = %d", refused.StatusCode)
	}
	select {
	case event := <-events:
		if event.name != "refused" || len(event.data) != 0 {
			t.Fatalf("event = %#v", event)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the waiting device never heard of the refusal")
	}
	missing, err := http.Get(server.URL + "/v1/auth/device/dsi_" + strings.Repeat("A", 43))
	if err != nil {
		t.Fatal(err)
	}
	missing.Body.Close()
	if missing.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown device request = %d", missing.StatusCode)
	}
}

func TestIntegrationTokenExchangesForAnIntegrationAccessToken(t *testing.T) {
	fixture := newBearerFixture(t)
	server := deviceSignInServer(fixture.database)
	defer server.Close()
	ctx := context.Background()
	space, err := fixture.database.TestingCreateSpace(ctx, fixture.userID, "Bots")
	if err != nil {
		t.Fatal(err)
	}
	token, hash, err := spaces.NewIntegrationToken()
	if err != nil {
		t.Fatal(err)
	}
	created, err := fixture.database.CreateSpaceIntegration(ctx, fixture.userID, space.ID,
		db.SpaceIntegrationInput{Name: "Deploy bot", Access: []string{"rooms.write"}, KeyPrefix: "app:board:"}, hash)
	if err != nil {
		t.Fatal(err)
	}
	response := postJSON(t, server.URL+"/v1/auth/integration", "", `{"token":"`+token+`"}`)
	var exchanged struct {
		AccessToken string            `json:"access_token"`
		ExpiresIn   int               `json:"expires_in"`
		Integration map[string]string `json:"integration"`
	}
	if err := json.NewDecoder(response.Body).Decode(&exchanged); err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("exchange = %d, %v", response.StatusCode, err)
	}
	response.Body.Close()
	if exchanged.ExpiresIn != 300 || exchanged.Integration["id"] != created.ID || exchanged.Integration["space_id"] != space.ID ||
		exchanged.Integration["name"] != "Deploy bot" {
		t.Fatalf("exchanged = %#v", exchanged)
	}
	signer, err := security.SessionSignerFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	claims, err := signer.VerifyIntegration(exchanged.AccessToken)
	if err != nil || claims.Subject != created.ID || claims.SpaceID != space.ID || claims.KeyPrefix != "app:board:" {
		t.Fatalf("claims = %#v, %v", claims, err)
	}
	request, err := http.NewRequest(http.MethodGet, server.URL+"/v1/me/sessions", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+exchanged.AccessToken)
	asPerson, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	asPerson.Body.Close()
	if asPerson.StatusCode != http.StatusUnauthorized {
		t.Fatalf("an integration reached a person's endpoint: %d", asPerson.StatusCode)
	}
	bad := postJSON(t, server.URL+"/v1/auth/integration", "", `{"token":"misty_int_`+strings.Repeat("0", 43)+`"}`)
	bad.Body.Close()
	if bad.StatusCode != http.StatusUnauthorized {
		t.Fatalf("an unknown token exchanged: %d", bad.StatusCode)
	}
}
