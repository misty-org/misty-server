package db

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestGitHubAppRepositoryProvenanceAndSingleUseHandoff(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	suffix := strings.ReplaceAll(uuid.NewString()[:12], "-", "")
	owner, err := database.CreateUserWithUsername("GitHub Owner", "gh_"+suffix, "gh-"+suffix+"@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	space := createTestSpace(t, database, ctx, owner.ID, "GitHub")
	installation, err := database.SaveGitHubAppInstallation(ctx, owner.ID, space.ID, GitHubAppInstallation{InstallationID: 9001, AccountID: 42, AccountLogin: "misty-org", AccountType: "Organization", RepositorySelection: "selected", Permissions: json.RawMessage(`{"contents":"write","issues":"write","pull_requests":"write"}`), Events: json.RawMessage(`["push","issues","pull_request"]`)})
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := database.CreateGitHubCodeWorkspace(ctx, owner.ID, space.ID, installation.ID, "native-workspace-9001", GitHubRepository{ID: 9010, FullName: "misty-org/misty", DefaultBranch: "main", CloneURL: "https://github.com/misty-org/misty.git", HTMLURL: "https://github.com/misty-org/misty", Private: true, Permissions: json.RawMessage(`{"pull":true,"push":true}`)})
	if err != nil {
		t.Fatal(err)
	}
	record := GitHubRepositoryRecord{WorkspaceID: workspace.ID, RepositoryID: 9010, RecordType: "pull_request", ExternalID: "17", Title: "Ship GitHub", State: "open", URL: "https://github.com/misty-org/misty/pull/17", Fingerprint: strings.Repeat("b", 64), Provenance: json.RawMessage(`{"source":"webhook","delivery_id":"delivery-17"}`)}
	if err := database.UpsertGitHubRepositoryRecord(ctx, record); err != nil {
		t.Fatal(err)
	}
	records, err := database.GitHubRepositoryRecords(ctx, owner.ID, space.ID, workspace.ID, "pull_request", 10)
	if err != nil || len(records) != 1 || records[0].Title != "Ship GitHub" {
		t.Fatalf("records=%#v err=%v", records, err)
	}
	fresh, err := database.BeginGitHubWebhookDelivery(ctx, "delivery-"+suffix, "pull_request", "opened", 9001, 9010, strings.Repeat("c", 64))
	if err != nil || !fresh {
		t.Fatalf("first delivery fresh=%v err=%v", fresh, err)
	}
	fresh, err = database.BeginGitHubWebhookDelivery(ctx, "delivery-"+suffix, "pull_request", "opened", 9001, 9010, strings.Repeat("c", 64))
	if err != nil || fresh {
		t.Fatalf("duplicate delivery fresh=%v err=%v", fresh, err)
	}
	handleHash := strings.Repeat("d", 64)
	if err := database.CreateGitHubCredentialHandoff(ctx, handleHash, owner.ID, space.ID, workspace.ID, time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	redeemed, userID, err := database.ConsumeGitHubCredentialHandoffByHandle(ctx, handleHash)
	if err != nil || redeemed.ID != workspace.ID || userID != owner.ID {
		t.Fatalf("redeemed=%#v user=%s err=%v", redeemed, userID, err)
	}
	if _, _, err := database.ConsumeGitHubCredentialHandoffByHandle(ctx, handleHash); err == nil {
		t.Fatal("single-use handoff replay succeeded")
	}
}
