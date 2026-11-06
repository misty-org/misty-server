package api

import (
	"context"
	"strings"
	"testing"

	"github.com/misty-org/misty-server/internal/integrations/composio"
)

type fakeAppConnections struct {
	accounts map[string][]composio.Account
	deleted  []string
}

func (f *fakeAppConnections) Accounts(_ context.Context, userID, _ string) ([]composio.Account, error) {
	return f.accounts[userID], nil
}

func (f *fakeAppConnections) DeleteAccount(_ context.Context, userID, id string) error {
	f.deleted = append(f.deleted, userID+"/"+id)
	return nil
}

func TestAccountPurgeDeletesEveryConnectedAppAtComposio(t *testing.T) {
	owner := composio.UserID("user-1")
	apps := &fakeAppConnections{accounts: map[string][]composio.Account{
		owner: {{ID: "ca_gmail", UserID: owner}, {ID: "ca_slack", UserID: owner}},
	}}
	s := &SpacesService{appConnections: apps}
	status := map[string]string{}
	if err := s.removeAppConnections(context.Background(), "user-1", status); err != nil {
		t.Fatal(err)
	}
	if strings.Join(apps.deleted, ",") != owner+"/ca_gmail,"+owner+"/ca_slack" || status["apps"] != "deleted_2" {
		t.Fatalf("deleted %v, status %v", apps.deleted, status)
	}
}

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
