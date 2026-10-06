package api

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

// Approval from another device: the new device commits to a nonce, the
// approver challenges once, the reveal must match the commitment, and only an
// approval with a valid root grant admits the device.
func TestDeviceApprovalFromAnotherDevice(t *testing.T) {
	f := newDeviceFixture(t)
	approver := f.register("Approver")
	if response := f.admit(approver, 1, []testDevice{approver}, f.root); response.Code != http.StatusOK {
		t.Fatalf("admit approver: %d", response.Code)
	}
	newcomer := f.register("Newcomer")

	nonce := make([]byte, 32)
	_, _ = rand.Read(nonce)
	commitment := sha256.Sum256(nonce)
	xPublic := base64.StdEncoding.EncodeToString(make([]byte, 32))
	request := signedRecord(newcomer.key, "misty.device.admission.v1", f.userID, newcomer.id, newcomer.public(), xPublic, hex.EncodeToString(commitment[:]), time.Now().Unix())
	created := f.signed(newcomer, http.MethodPost, "/devices/"+newcomer.id+"/admission-requests", map[string]any{"request": request})
	if created.Code != http.StatusCreated {
		t.Fatalf("create request: %d %s", created.Code, created.Body.String())
	}
	var body struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(created.Body.Bytes(), &body)
	path := func(device testDevice, suffix string) string {
		return "/devices/" + device.id + "/admission-requests/" + body.ID + suffix
	}
	approverNonce := base64.StdEncoding.EncodeToString(nonce[:32])
	// The new device cannot challenge its own request.
	if response := f.signed(newcomer, http.MethodPost, path(newcomer, "/challenge"), map[string]any{"x25519Public": xPublic, "nonce": approverNonce}); response.Code != http.StatusForbidden {
		t.Fatalf("pending device challenged: %d", response.Code)
	}
	if response := f.signed(approver, http.MethodPost, path(approver, "/challenge"), map[string]any{"x25519Public": xPublic, "nonce": approverNonce}); response.Code != http.StatusOK {
		t.Fatalf("challenge: %d %s", response.Code, response.Body.String())
	}
	// One challenge per request.
	if response := f.signed(approver, http.MethodPost, path(approver, "/challenge"), map[string]any{"x25519Public": xPublic, "nonce": approverNonce}); response.Code != http.StatusConflict {
		t.Fatalf("second challenge: %d", response.Code)
	}
	wrong := make([]byte, 32)
	if response := f.signed(newcomer, http.MethodPost, path(newcomer, "/reveal"), map[string]any{"nonce": base64.StdEncoding.EncodeToString(wrong)}); response.Code != http.StatusBadRequest {
		t.Fatalf("a reveal that misses the commitment: %d", response.Code)
	}
	if response := f.signed(newcomer, http.MethodPost, path(newcomer, "/reveal"), map[string]any{"nonce": base64.StdEncoding.EncodeToString(nonce)}); response.Code != http.StatusOK {
		t.Fatalf("reveal: %d %s", response.Code, response.Body.String())
	}
	now := time.Now().Unix()
	sealed := base64.StdEncoding.EncodeToString(make([]byte, 60))
	_, wrongRoot, _ := ed25519.GenerateKey(rand.Reader)
	approve := func(root ed25519.PrivateKey) int {
		grant := signedRecord(root, "misty.device.grant.v2", f.userID, f.vaultID, newcomer.id, 1, newcomer.public(), "", now, approver.id)
		list := signedRecord(root, "misty.device.list.v1", f.userID, f.vaultID, 2, 1, pairs(approver, newcomer), [][2]string{}, now)
		return f.signed(approver, http.MethodPost, path(approver, "/approve"), map[string]any{"grant": grant, "list": list, "sealedRoot": sealed}).Code
	}
	if code := approve(wrongRoot); code != http.StatusBadRequest {
		t.Fatalf("approval signed by another root: %d", code)
	}
	if code := approve(f.root); code != http.StatusOK {
		t.Fatalf("approve: %d", code)
	}
	status := f.signed(newcomer, http.MethodGet, path(newcomer, ""), nil)
	var state struct {
		State      string `json:"state"`
		SealedRoot string `json:"sealedRoot"`
	}
	_ = json.Unmarshal(status.Body.Bytes(), &state)
	if state.State != "approved" || state.SealedRoot != sealed {
		t.Fatalf("approved request = %+v", state)
	}
	// The newcomer is added now and may claim its jobs.
	if response := f.signed(newcomer, http.MethodPost, "/devices/"+newcomer.id+"/workflow-node-jobs/claim", map[string]any{"protocolVersion": 2}); response.Code == http.StatusForbidden {
		t.Fatalf("approved device still pending")
	}
}
