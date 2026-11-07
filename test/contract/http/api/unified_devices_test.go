package api

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/misty-org/misty-server/internal/platform/db"
	. "github.com/misty-org/misty-server/internal/platform/httpapi"
)

// Signed-in sessions register with proof of their key and are trusted at once; policies and run
// grants are signed by keys the server never holds.

type deviceFixture struct {
	t        *testing.T
	database *db.Database
	router   *chi.Mux
	token    string
	userID   string
}

type testDevice struct {
	id  string
	key ed25519.PrivateKey
}

func newDeviceFixture(t *testing.T) *deviceFixture {
	database := openPresenceTestDatabase(t)
	user, err := database.CreateUserWithUsername("dev"+strings.ReplaceAll(uuid.NewString()[:13], "-", ""), uniqueTestEmail("dev"), "password123")
	if err != nil {
		t.Fatal(err)
	}
	service := NewDevicesService(database)
	router := chi.NewRouter()
	router.Post("/devices", service.RegisterDevice())
	router.Get("/devices", service.ListDevices())
	router.Put("/devices/{deviceID}/policy", service.DeviceAuthenticated(service.StoreDevicePolicy()))
	router.Post("/devices/{deviceID}/remove-device", service.DeviceAuthenticated(service.RemoveDevice()))
	return &deviceFixture{t: t, database: database, router: router, token: newConversationTestBearerToken(t, database, user.ID), userID: user.ID}
}

func signedRecord(key ed25519.PrivateKey, fields ...any) map[string]string {
	payload, _ := json.Marshal(fields)
	return map[string]string{"payload": base64.StdEncoding.EncodeToString(payload), "signature": base64.StdEncoding.EncodeToString(ed25519.Sign(key, payload))}
}

func (f *deviceFixture) register(name string) testDevice {
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	device := testDevice{key: key}
	response := f.registerKey(key, name, true)
	if response.Code != http.StatusCreated {
		f.t.Fatalf("register: %d %s", response.Code, response.Body.String())
	}
	var body struct {
		ID             string `json:"id"`
		AdmissionState string `json:"admissionState"`
	}
	_ = json.Unmarshal(response.Body.Bytes(), &body)
	if body.AdmissionState != "admitted" {
		f.t.Fatalf("a signed-in session is trusted at once, got %q", body.AdmissionState)
	}
	device.id = body.ID
	return device
}

func (f *deviceFixture) registerKey(key ed25519.PrivateKey, name string, validProof bool) *httptest.ResponseRecorder {
	public := key.Public().(ed25519.PublicKey)
	publicText := base64.StdEncoding.EncodeToString(public)
	endpoint := hex.EncodeToString(public)
	issuedAt := time.Now().Unix()
	message, _ := json.Marshal([]any{"misty.device.register.v1", f.userID, publicText, endpoint, issuedAt})
	signer := key
	if !validProof {
		_, signer, _ = ed25519.GenerateKey(rand.Reader)
	}
	return performConversationRequest(f.t, f.router, http.MethodPost, "/devices", f.token, map[string]any{
		"name": name, "publicKey": publicText, "platform": "macos", "p2pEndpointId": endpoint,
		"issuedAt": issuedAt, "proof": base64.StdEncoding.EncodeToString(ed25519.Sign(signer, message)),
	})
}

func (f *deviceFixture) signed(device testDevice, method, path string, body any) *httptest.ResponseRecorder {
	payload, _ := json.Marshal(body)
	if body == nil {
		payload = nil
	}
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	nonce := base64.StdEncoding.EncodeToString([]byte(uuid.NewString()))
	canonical := DeviceSignaturePayload(method, path, timestamp, nonce, payload)
	signature := ed25519.Sign(device.key, []byte("misty.device.request.v1\n"+canonical))
	request := httptest.NewRequest(method, path, bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(&http.Cookie{Name: SessionCookieName, Value: f.token})
	request.Header.Set("X-Misty-Device-Timestamp", timestamp)
	request.Header.Set("X-Misty-Device-Nonce", nonce)
	request.Header.Set("X-Misty-Device-Signature", base64.StdEncoding.EncodeToString(signature))
	recorder := httptest.NewRecorder()
	f.router.ServeHTTP(recorder, request)
	return recorder
}

func TestASignedInSessionIsTrustedAtOnce(t *testing.T) {
	f := newDeviceFixture(t)
	if response := f.registerKey(func() ed25519.PrivateKey { _, k, _ := ed25519.GenerateKey(rand.Reader); return k }(), "No proof", false); response.Code != http.StatusBadRequest {
		t.Fatalf("registration without proof of the key: %d", response.Code)
	}
	first := f.register("First")
	second := f.register("Second")
	if response := f.signed(second, http.MethodPost, "/devices/"+second.id+"/workflow-node-jobs/claim", map[string]any{"protocolVersion": "misty-device/3"}); response.Code == http.StatusForbidden {
		t.Fatalf("a new session could not poll its own jobs: %d", response.Code)
	}
	// A removed key can never register again, and stops authenticating.
	if response := f.signed(first, http.MethodPost, "/devices/"+first.id+"/remove-device", map[string]any{"targetDeviceId": second.id}); response.Code != http.StatusOK {
		t.Fatalf("remove: %d %s", response.Code, response.Body.String())
	}
	if response := f.registerKey(second.key, "Back again", true); response.Code != http.StatusForbidden {
		t.Fatalf("a removed key registered again: %d %s", response.Code, response.Body.String())
	}
	if response := f.signed(second, http.MethodPost, "/devices/"+second.id+"/workflow-node-jobs/claim", map[string]any{"protocolVersion": "misty-device/3"}); response.Code == http.StatusOK {
		t.Fatalf("a removed device still authenticated: %d", response.Code)
	}
}

func policyRecord(key ed25519.PrivateKey, userID, deviceID string, version int64, files string) map[string]string {
	return signedRecord(key, "misty.device.policy.v1", userID, deviceID, version, files, false, []string{"browser", "folders"}, [][2]string{{"scope-1", "Reports"}}, time.Now().Unix())
}

func TestDevicePolicyIsSignedOnlyByItsDevice(t *testing.T) {
	f := newDeviceFixture(t)
	first := f.register("First")
	second := f.register("Second")
	if response := f.signed(first, http.MethodPut, "/devices/"+first.id+"/policy", map[string]any{"policy": policyRecord(first.key, f.userID, first.id, 5, "view")}); response.Code != http.StatusOK {
		t.Fatalf("own policy: %d %s", response.Code, response.Body.String())
	}
	// The second device cannot set the first device's permissions.
	if response := f.signed(first, http.MethodPut, "/devices/"+first.id+"/policy", map[string]any{"policy": policyRecord(second.key, f.userID, first.id, 6, "edit")}); response.Code != http.StatusBadRequest {
		t.Fatalf("policy signed by another device: %d", response.Code)
	}
	// A replayed older policy cannot undo a newer one.
	if response := f.signed(first, http.MethodPut, "/devices/"+first.id+"/policy", map[string]any{"policy": policyRecord(first.key, f.userID, first.id, 4, "edit")}); response.Code != http.StatusConflict {
		t.Fatalf("older policy: %d", response.Code)
	}
}
