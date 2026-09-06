package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeAvatarPNGFixtures(t *testing.T) {
	raw, err := os.ReadFile("../../../docs/migration/fixtures/avatar-png.json")
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
			_, valid := TestingReadAvatarPNG(response, httptest.NewRequest("PUT", "/me/avatar", bytes.NewReader(data)))
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

func TestNativeAvatarFilesystemRoundTrip(t *testing.T) {
	directory := os.Getenv("MISTY_TEST_AVATAR_DIRECTORY")
	if directory == "" {
		t.Skip("Run scripts/migration/check-avatar-storage.mjs for the disposable cross-runtime proof")
	}
	if !strings.HasPrefix(filepath.Base(directory), "misty-avatar-compat-") {
		t.Fatal("requires an explicitly disposable avatar compatibility directory")
	}
	expected, err := os.ReadFile(filepath.Join(directory, "native.expected"))
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewLocalLibraryObjectStore(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"avatars/avatar_12345678", "library/native_12345678"} {
		body, metadata, err := store.Open(context.Background(), key)
		if err != nil {
			t.Fatal(err)
		}
		actual, err := io.ReadAll(io.LimitReader(body, 101))
		body.Close()
		if err != nil {
			t.Fatal(err)
		}
		hash := sha256.Sum256(actual)
		if !bytes.Equal(actual, expected) || metadata.ByteSize != int64(len(actual)) || metadata.SHA256 != hex.EncodeToString(hash[:]) || metadata.MIMEType != "image/png" {
			t.Fatal("native filesystem object did not retain its bytes and metadata")
		}
	}
	data := []byte("Go local image bytes")
	hash := sha256.Sum256(data)
	for _, key := range []string{"avatars/user_12345678", "library/legacy_12345678"} {
		if err := store.Put(context.Background(), key, bytes.NewReader(data), LibraryObjectMetadata{ByteSize: int64(len(data)), SHA256: hex.EncodeToString(hash[:]), MIMEType: "image/png"}); err != nil {
			t.Fatal(err)
		}
	}
}
