package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/httpapi"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestMemoryLibraryObjectInventoryPagesWithoutOpeningBodies(t *testing.T) {
	store := NewMemoryLibraryObjectStore()
	for _, key := range []string{
		"library/reconcile00000001",
		"library/reconcile00000002",
		"avatars/reconcile00000003",
	} {
		data := []byte(key)
		sum := sha256Hex(data)
		if err := store.Put(context.Background(), key, bytes.NewReader(data), LibraryObjectMetadata{
			ByteSize: int64(len(data)), SHA256: sum, MIMEType: "image/png",
		}); err != nil {
			t.Fatal(err)
		}
	}

	first, err := store.List(context.Background(), "library/", "", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Objects) != 1 || first.NextCursor == "" {
		t.Fatalf("first page = %#v", first)
	}
	second, err := store.List(context.Background(), "library/", first.NextCursor, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Objects) != 1 || second.Objects[0].Key == first.Objects[0].Key {
		t.Fatalf("second page = %#v", second)
	}
	for _, page := range []LibraryObjectPage{first, second} {
		if !strings.HasPrefix(page.Objects[0].Key, "library/") ||
			page.Objects[0].ByteSize < 1 ||
			page.Objects[0].LastModified.IsZero() {
			t.Fatalf("unsafe inventory entry = %#v", page.Objects[0])
		}
	}
}

func TestObjectMatchesExpectationRequiresSizeAndChecksum(t *testing.T) {
	expected := db.LibraryObjectExpectation{
		ObjectKey: "library/reconcile00000001",
		ByteSize:  12,
		SHA256:    strings.Repeat("a", 64),
	}
	if !TestingObjectMatchesExpectation(LibraryObjectMetadata{
		ByteSize: 12, SHA256: strings.Repeat("a", 64),
	}, expected) {
		t.Fatal("matching immutable metadata was rejected")
	}
	if TestingObjectMatchesExpectation(LibraryObjectMetadata{
		ByteSize: 13, SHA256: strings.Repeat("a", 64),
	}, expected) {
		t.Fatal("size mismatch was accepted")
	}
	if TestingObjectMatchesExpectation(LibraryObjectMetadata{
		ByteSize: 12, SHA256: strings.Repeat("b", 64),
	}, expected) {
		t.Fatal("checksum mismatch was accepted")
	}
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
