package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/httpapi"
)

func TestFFprobeLibraryMetadataExtractor(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg is not installed")
	}
	path := t.TempDir() + "/metadata.mp4"
	command := exec.Command(ffmpeg, "-nostdin", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "color=c=red:size=80x60:rate=20:duration=0.5", "-metadata", "creation_time=2026-07-15T12:34:56Z", "-metadata", "location=+37.7749-122.4194/", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-y", path)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("create metadata fixture: %v: %s", err, output)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	store := NewMemoryLibraryObjectStore()
	key := "library/metadata-test-object"
	if err := store.Put(context.Background(), key, bytes.NewReader(data), LibraryObjectMetadata{ByteSize: int64(len(data)), SHA256: hex.EncodeToString(digest[:]), MIMEType: "video/mp4"}); err != nil {
		t.Fatal(err)
	}
	extractor, err := NewFFprobeLibraryMetadataExtractor(ffmpeg)
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := extractor.Extract(context.Background(), store, key, int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if metadata["width"] != 80 || metadata["height"] != 60 || metadata["duration"] == nil || metadata["capture_timestamp"] != "2026-07-15T12:34:56Z" {
		t.Fatalf("extracted metadata = %#v", metadata)
	}
}

func TestParseISO6709(t *testing.T) {
	latitude, longitude, ok := TestingParseISO6709("+37.7749-122.4194/")
	if !ok || latitude != 37.7749 || longitude != -122.4194 {
		t.Fatalf("parseISO6709() = %f,%f,%v", latitude, longitude, ok)
	}
}
