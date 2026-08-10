package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/httpapi"
)

func TestLocalLibraryObjectStorePersistsAcrossInstances(t *testing.T) {
	root := t.TempDir()
	first, err := NewLocalLibraryObjectStore(root)
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("persistent demo fixture")
	digest := sha256.Sum256(data)
	metadata := LibraryObjectMetadata{
		ByteSize: int64(len(data)), SHA256: hex.EncodeToString(digest[:]), MIMEType: "text/plain",
	}
	const key = "library/demoasset1234"
	if err := first.Put(context.Background(), key, bytes.NewReader(data), metadata); err != nil {
		t.Fatal(err)
	}
	second, err := NewLocalLibraryObjectStore(root)
	if err != nil {
		t.Fatal(err)
	}
	reader, actual, err := second.Open(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	got, _ := io.ReadAll(reader)
	if !bytes.Equal(got, data) || actual != metadata {
		t.Fatalf("Open() = %q, %#v", got, actual)
	}
	if err := second.Delete(context.Background(), key); err != nil {
		t.Fatal(err)
	}
	if _, err := first.Head(context.Background(), key); err != ErrLibraryObjectNotFound {
		t.Fatalf("Head after Delete = %v", err)
	}
}
