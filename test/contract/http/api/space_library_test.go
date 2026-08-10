package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/httpapi"
)

func TestInspectLibraryContentVerifiesSafeContent(t *testing.T) {
	data := []byte("Misty Library plain text")
	detected, intrinsic, err := TestingInspectLibraryContent(bytes.NewReader(data), int64(len(data)), "notes.txt", "text/plain")
	if err != nil {
		t.Fatalf("inspectLibraryContent() error = %v", err)
	}
	if !strings.HasPrefix(detected, "text/plain") {
		t.Fatalf("detected MIME = %q, want text/plain", detected)
	}
	want := sha256.Sum256(data)
	if intrinsic["sha256"] != hex.EncodeToString(want[:]) {
		t.Fatalf("sha256 = %v, want %x", intrinsic["sha256"], want)
	}
}

func TestHTTPLibraryPeopleProcessorUsesPrivateBoundedContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer processor-token" || r.Header.Get("X-Misty-Detect-People") != "true" || r.Header.Get("X-Misty-Detect-Pets") != "false" || r.Header.Get("Content-Type") != "image/jpeg" {
			t.Fatalf("processor headers = %#v", r.Header)
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != "private-image" {
			t.Fatalf("processor body = %q", body)
		}
		_, _ = io.WriteString(w, `{"detections":[{"kind":"person","confidence":0.98,"bounds":{"x":0.1},"embedding":[1,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0]}]}`)
	}))
	defer server.Close()
	processor, err := NewHTTPLibraryPeopleProcessor(server.URL, "processor-token")
	if err != nil {
		t.Fatal(err)
	}
	detections, err := processor.Analyze(context.Background(), strings.NewReader("private-image"), "image/jpeg", int64(len("private-image")), true, false)
	if err != nil || len(detections) != 1 || detections[0].Kind != "person" || len(detections[0].Embedding) != 16 {
		t.Fatalf("Analyze() = %#v, %v", detections, err)
	}
	if _, err := NewHTTPLibraryPeopleProcessor("http://example.com/processor", ""); err == nil {
		t.Fatal("processor accepted non-loopback plain HTTP")
	}
}

func TestInspectLibraryContentRejectsDangerousAndMalwareContent(t *testing.T) {
	if _, _, err := TestingInspectLibraryContent(strings.NewReader("#!/bin/sh"), 9, "run.sh", "text/plain"); TestingLibraryInspectionCode(err) != "dangerous_file_type" {
		t.Fatalf("script error = %v, want dangerous_file_type", err)
	}
	eicar := []byte("prefix EICAR-STANDARD-ANTIVIRUS-TEST-FILE suffix")
	if _, _, err := TestingInspectLibraryContent(bytes.NewReader(eicar), int64(len(eicar)), "test.txt", "text/plain"); !errors.Is(err, TestingErrLibraryMalware) {
		t.Fatalf("EICAR error = %v, want malware", err)
	}
}

func TestMemoryLibraryObjectStoreEnforcesExactObject(t *testing.T) {
	store := NewMemoryLibraryObjectStore()
	data := []byte("private library object")
	sum := sha256.Sum256(data)
	metadata := LibraryObjectMetadata{ByteSize: int64(len(data)), SHA256: hex.EncodeToString(sum[:]), MIMEType: "text/plain"}
	if err := store.Put(context.Background(), "library/12345678", bytes.NewReader(data), metadata); err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	reader, actual, err := store.Open(context.Background(), "library/12345678")
	if err != nil || actual != metadata {
		t.Fatalf("Open() = %#v, %v", actual, err)
	}
	defer reader.Close()
	got, _ := io.ReadAll(reader)
	if !bytes.Equal(got, data) {
		t.Fatalf("Open() bytes = %q, want %q", got, data)
	}
	bad := metadata
	bad.SHA256 = strings.Repeat("0", 64)
	if err := store.Put(context.Background(), "library/abcdefgh", bytes.NewReader(data), bad); err == nil {
		t.Fatal("Put() accepted mismatched checksum")
	}
}
