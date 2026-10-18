package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"strings"
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/httpapi"
)



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
