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
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	. "github.com/kannachi323/misty/server/internal/platform/httpapi"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

// Unified devices (docs/design/devices/BRIEF.md): admission needs a session AND
// a vault-root signature; trust records are signed by keys the server never holds.

type deviceFixture struct {
	t        *testing.T
	database *db.Database
	router   *chi.Mux
	token    string
	userID   string
	vaultID  string
	root     ed25519.PrivateKey
}

type testDevice struct {
	id  string
	key ed25519.PrivateKey
}

func (d testDevice) public() string {
	return base64.StdEncoding.EncodeToString(d.key.Public().(ed25519.PublicKey))
}

func newDeviceFixture(t *testing.T) *deviceFixture {
	database := openPresenceTestDatabase(t)
	user, err := database.CreateUserWithUsername("Device owner", "dev"+strings.ReplaceAll(uuid.NewString()[:13], "-", ""), uniqueTestEmail("dev"), "password123")
	if err != nil {
		t.Fatal(err)
	}
	rootPublic, rootPrivate, _ := ed25519.GenerateKey(rand.Reader)
	vaultID := uuid.NewString()
	if _, err := database.Conn.Exec(`INSERT INTO browser_sync_vaults(user_id,vault_id,root_public_key,key_envelope) VALUES($1,$2,$3,'{}'::jsonb)`, user.ID, vaultID, []byte(rootPublic)); err != nil {
		t.Fatal(err)
	}
	service := NewAgentsService(database)
	router := chi.NewRouter()
	router.Post("/devices", service.RegisterDevice())
	router.Get("/devices", service.ListDevices())
	router.Get("/devices/trust", service.DeviceTrust())
	router.Post("/devices/{deviceID}/admit", service.DeviceAuthenticated(service.AdmitDevice()))
	router.Put("/devices/{deviceID}/policy", service.DeviceAuthenticated(service.StoreDevicePolicy()))
	router.Post("/devices/{deviceID}/remove-device", service.DeviceAuthenticated(service.RemoveDevice()))
	router.Post("/devices/{deviceID}/workflow-node-jobs/claim", service.DeviceAuthenticated(service.ClaimWorkflowNodeJob()))
	router.Post("/devices/{deviceID}/admission-requests", service.DeviceAuthenticated(service.CreateAdmissionRequest()))
	router.Get("/devices/{deviceID}/admission-requests/{requestID}", service.DeviceAuthenticated(service.AdmissionRequest()))
	router.Post("/devices/{deviceID}/admission-requests/{requestID}/challenge", service.DeviceAuthenticated(service.ChallengeAdmission()))
	router.Post("/devices/{deviceID}/admission-requests/{requestID}/reveal", service.DeviceAuthenticated(service.RevealAdmission()))
	router.Post("/devices/{deviceID}/admission-requests/{requestID}/approve", service.DeviceAuthenticated(service.ApproveAdmission()))
	return &deviceFixture{t: t, database: database, router: router, token: newConversationTestBearerToken(t, database, user.ID), userID: user.ID, vaultID: vaultID, root: rootPrivate}
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
	if body.AdmissionState != "pending" {
		f.t.Fatalf("a new device must start pending, got %q", body.AdmissionState)
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
	canonical := TestingDeviceSignaturePayload(method, path, timestamp, nonce, payload)
	signature := ed25519.Sign(device.key, []byte("misty.device.request.v1\n"+canonical))
	request := httptest.NewRequest(method, path, bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(&http.Cookie{Name: TestingSessionCookieName, Value: f.token})
	request.Header.Set("X-Misty-Device-Timestamp", timestamp)
	request.Header.Set("X-Misty-Device-Nonce", nonce)
	request.Header.Set("X-Misty-Device-Signature", base64.StdEncoding.EncodeToString(signature))
	recorder := httptest.NewRecorder()
	f.router.ServeHTTP(recorder, request)
	return recorder
}

func pairs(devices ...testDevice) [][2]string {
	out := [][2]string{}
	for _, device := range devices {
		out = append(out, [2]string{device.id, device.public()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i][0] < out[j][0] })
	return out
}

func (f *deviceFixture) admit(device testDevice, version int64, admitted []testDevice, root ed25519.PrivateKey) *httptest.ResponseRecorder {
	now := time.Now().Unix()
	grant := signedRecord(root, "misty.device.grant.v2", f.userID, f.vaultID, device.id, 1, device.public(), "", now, "")
	list := signedRecord(root, "misty.device.list.v1", f.userID, f.vaultID, version, 1, pairs(admitted...), [][2]string{}, now)
	return f.signed(device, http.MethodPost, "/devices/"+device.id+"/admit", map[string]any{"grant": grant, "list": list})
}

func TestUnifiedDeviceAdmissionNeedsVaultRootSignature(t *testing.T) {
	f := newDeviceFixture(t)
	if response := f.registerKey(func() ed25519.PrivateKey { _, k, _ := ed25519.GenerateKey(rand.Reader); return k }(), "No proof", false); response.Code != http.StatusBadRequest {
		t.Fatalf("registration without proof of the key: %d", response.Code)
	}
	first := f.register("First")
	_, wrongRoot, _ := ed25519.GenerateKey(rand.Reader)
	if response := f.admit(first, 1, []testDevice{first}, wrongRoot); response.Code != http.StatusBadRequest {
		t.Fatalf("a grant from another root must be refused: %d %s", response.Code, response.Body.String())
	}
	if response := f.admit(first, 1, []testDevice{first}, f.root); response.Code != http.StatusOK {
		t.Fatalf("admit: %d %s", response.Code, response.Body.String())
	}
	second := f.register("Second")
	// A pending device receives no agent jobs.
	if response := f.signed(second, http.MethodPost, "/devices/"+second.id+"/workflow-node-jobs/claim", map[string]any{"protocolVersion": 2}); response.Code != http.StatusForbidden {
		t.Fatalf("pending device claimed jobs: %d", response.Code)
	}
	// A list that is not exactly the next version is refused.
	if response := f.admit(second, 1, []testDevice{first, second}, f.root); response.Code != http.StatusConflict {
		t.Fatalf("stale list version: %d %s", response.Code, response.Body.String())
	}
	// A list that drops a device without removing it is refused.
	if response := f.admit(second, 2, []testDevice{second}, f.root); response.Code != http.StatusConflict {
		t.Fatalf("list dropping a device: %d %s", response.Code, response.Body.String())
	}
	if response := f.admit(second, 2, []testDevice{first, second}, f.root); response.Code != http.StatusOK {
		t.Fatalf("admit second: %d %s", response.Code, response.Body.String())
	}
	trust := performConversationRequest(t, f.router, http.MethodGet, "/devices/trust", f.token, nil)
	var body struct {
		List struct {
			Version int64 `json:"version"`
		} `json:"list"`
	}
	_ = json.Unmarshal(trust.Body.Bytes(), &body)
	if body.List.Version != 2 {
		t.Fatalf("trust list version = %d", body.List.Version)
	}
	// Removing an added device needs the root-signed list without it; its key
	// can never register again.
	now := time.Now().Unix()
	removal := signedRecord(f.root, "misty.device.list.v1", f.userID, f.vaultID, 3, 1, pairs(first), pairs(second), now)
	if response := f.signed(first, http.MethodPost, "/devices/"+first.id+"/remove-device", map[string]any{"targetDeviceId": second.id}); response.Code != http.StatusConflict {
		t.Fatalf("removing an added device without a list: %d", response.Code)
	}
	if response := f.signed(first, http.MethodPost, "/devices/"+first.id+"/remove-device", map[string]any{"targetDeviceId": second.id, "list": removal}); response.Code != http.StatusOK {
		t.Fatalf("remove: %d %s", response.Code, response.Body.String())
	}
	if response := f.registerKey(second.key, "Back again", true); response.Code != http.StatusForbidden {
		t.Fatalf("a removed key registered again: %d %s", response.Code, response.Body.String())
	}
	if response := f.signed(second, http.MethodPost, "/devices/"+second.id+"/workflow-node-jobs/claim", map[string]any{"protocolVersion": 2}); response.Code != http.StatusUnauthorized {
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

func runGrant(key ed25519.PrivateKey, userID, requester, target string, capabilities, scopes []string) map[string]string {
	now := time.Now().Unix()
	return signedRecord(key, "misty.device.run-grant.v1", userID, "rungrant_"+uuid.NewString(), requester, target, "", capabilities, scopes, now, now+3600)
}

func TestRunGrantsLimitWhatAgentsMayDoOnADevice(t *testing.T) {
	f := newDeviceFixture(t)
	first := f.register("First")
	second := f.register("Second")
	if response := f.admit(first, 1, []testDevice{first}, f.root); response.Code != http.StatusOK {
		t.Fatalf("admit: %d", response.Code)
	}
	if response := f.admit(second, 2, []testDevice{first, second}, f.root); response.Code != http.StatusOK {
		t.Fatalf("admit: %d", response.Code)
	}
	check := func(contexts []map[string]any) error {
		raw, _ := json.Marshal(contexts)
		return TestingVerifyDeviceContextGrants(t.Context(), f.database, f.userID, "", raw)
	}
	folder := func(device testDevice, grant map[string]string) map[string]any {
		return map[string]any{"device_id": device.id, "kind": "local_folder", "opaque_ref": "scope-1", "capabilities": []string{"files.list", "files.read"}, "run_grant": grant}
	}
	if err := check([]map[string]any{folder(first, nil)}); err == nil {
		t.Fatal("a device context without a grant was accepted")
	}
	if err := check([]map[string]any{folder(first, runGrant(first.key, f.userID, first.id, first.id, []string{"files.list", "files.read"}, []string{"scope-1"}))}); err != nil {
		t.Fatalf("own grant: %v", err)
	}
	// The grant cannot be widened: it named only listing.
	if err := check([]map[string]any{folder(first, runGrant(first.key, f.userID, first.id, first.id, []string{"files.list"}, []string{"scope-1"}))}); err == nil {
		t.Fatal("a context wider than its grant was accepted")
	}
	// Signed by a key other than the named requester.
	if err := check([]map[string]any{folder(second, runGrant(second.key, f.userID, first.id, second.id, []string{"files.list", "files.read"}, []string{"scope-1"}))}); err == nil {
		t.Fatal("a grant with a forged requester was accepted")
	}
	// Another device only with its own policy allowing it.
	cross := folder(second, runGrant(first.key, f.userID, first.id, second.id, []string{"files.list", "files.read"}, []string{"scope-1"}))
	if err := check([]map[string]any{cross}); err == nil {
		t.Fatal("cross-device work without the target's policy was accepted")
	}
	if response := f.signed(second, http.MethodPut, "/devices/"+second.id+"/policy", map[string]any{"policy": policyRecord(second.key, f.userID, second.id, 1, "view")}); response.Code != http.StatusOK {
		t.Fatalf("policy: %d", response.Code)
	}
	if err := check([]map[string]any{cross}); err != nil {
		t.Fatalf("cross-device work the target allows: %v", err)
	}
	// Only a device can open its own inbox.
	inbox := map[string]any{"device_id": second.id, "kind": "inbox", "opaque_ref": "inbox:" + second.id, "capabilities": []string{"files.receive"},
		"run_grant": runGrant(first.key, f.userID, first.id, second.id, []string{"files.receive"}, []string{"inbox:" + second.id})}
	if err := check([]map[string]any{inbox}); err == nil {
		t.Fatal("one device opened another device's inbox")
	}
}
