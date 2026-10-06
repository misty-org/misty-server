package api

import (
	"encoding/json"
	"os"
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/httpapi"
)

// Records the desktop app signs (testdata/device_records.json, written by
// src-tauri's device_record_fixture test) parse and verify on the server.
func TestDesktopSignedDeviceRecordsVerifyOnTheServer(t *testing.T) {
	raw, err := os.ReadFile("testdata/device_records.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture DeviceRecordFixture
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	if err := TestingVerifyDeviceRecordFixture(fixture); err != nil {
		t.Fatal(err)
	}
	// A changed byte anywhere breaks it.
	tampered := fixture
	tampered.Policy.Signature = fixture.Grant.Signature
	if err := TestingVerifyDeviceRecordFixture(tampered); err == nil {
		t.Fatal("a policy with another record's signature verified")
	}
}
