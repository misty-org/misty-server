package api

import (
	"context"
	"strings"
	"testing"
)

func TestAccountPurgeDeletesStoredObjectsAndToleratesMissingOnes(t *testing.T) {
	store := NewMemoryLibraryObjectStore()
	s := &SpacesService{avatarStore: store}
	ctx := context.Background()
	if err := store.Put(ctx, "library/purge-test", strings.NewReader("x"), LibraryObjectMetadata{ByteSize: 1, SHA256: "2d711642b726b04401627ca9fbac32f5c8530fb1903cc4db02258717921a4881", MIMEType: "text/plain"}); err != nil {
		t.Fatal(err)
	}
	if err := s.deleteStoredObjects(ctx, []string{"library/purge-test", "library/never-stored", ""}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Head(ctx, "library/purge-test"); err == nil {
		t.Fatal("the object is still stored")
	}
}
