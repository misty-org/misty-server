package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	. "github.com/misty-org/misty-server/internal/platform/httpapi"
	"net/http/httptest"
	"os"
	"testing"
)

func TestNativeAvatarPNGFixtures(t *testing.T) {
	raw, err := os.ReadFile("../../../fixtures/compatibility/avatar-png.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []struct {
		Name   string
		Base64 string
		Status int
	}
	if err := json.Unmarshal(raw, &fixtures); err != nil {
		t.Fatal(err)
	}
	for _, fixture := range fixtures {
		t.Run(fixture.Name, func(t *testing.T) {
			data, err := base64.StdEncoding.DecodeString(fixture.Base64)
			if err != nil {
				t.Fatal(err)
			}
			response := httptest.NewRecorder()
			_, valid := ReadAvatarPNG(response, httptest.NewRequest("PUT", "/me/avatar", bytes.NewReader(data)))
			status := response.Code
			if valid {
				status = 200
			}
			if status != fixture.Status {
				t.Fatalf("status=%d want=%d", status, fixture.Status)
			}
		})
	}
}
