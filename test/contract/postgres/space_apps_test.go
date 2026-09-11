package db

import (
	"context"
	"errors"
	. "github.com/kannachi323/misty/server/internal/platform/postgres"
	"github.com/kannachi323/misty/server/internal/platform/security"
	"testing"
)

func TestSpaceAppsRetainContentAndRevokeSessions(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	user, err := database.CreateUser("Space app owner", "space-app-owner@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	space := createTestSpace(t, database, ctx, user.ID, "Family")
	other := createTestSpace(t, database, ctx, user.ID, "Research")
	spec := AppInstallSpec{ID: "journal", Version: "1.0.0", PermissionVersion: 1, Scopes: []string{"notes.read", "storage.read", "storage.write"}}
	app, err := database.InstallSpaceApp(ctx, user.ID, space.ID, spec, nil)
	if err != nil {
		t.Fatal(err)
	}
	if app.SpaceID != space.ID || app.State != "installed" {
		t.Fatalf("unexpected app: %#v", app)
	}
	apps, err := database.SpaceApps(ctx, user.ID, other.ID)
	if err != nil || len(apps) != 0 {
		t.Fatalf("other space apps: %#v %v", apps, err)
	}
	token := security.HashToken("space-app-token")
	session, err := database.CreateAppRuntimeSession(ctx, user.ID, "journal", token, space.ID, AppRuntimeSessionTTL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = database.PutAppPersonalRecord(ctx, *session, "draft", []byte(`{"text":"keep me"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err = database.CreateAppRuntimeSession(ctx, user.ID, "journal", security.HashToken("wrong-space"), other.ID, AppRuntimeSessionTTL); !errors.Is(err, ErrAppNotInstalled) {
		t.Fatalf("other space session: %v", err)
	}
	removed, err := database.RemoveSpaceApp(ctx, user.ID, space.ID, "journal")
	if err != nil || removed.State != "recoverable" {
		t.Fatalf("remove: %#v %v", removed, err)
	}
	if live, err := database.AppRuntimeSessionByToken(ctx, token); err != nil || live != nil {
		t.Fatalf("old token survives: %#v %v", live, err)
	}
	if _, err = database.InstallSpaceApp(ctx, user.ID, space.ID, spec, nil); err != nil {
		t.Fatal(err)
	}
	if live, err := database.AppRuntimeSessionByToken(ctx, token); err != nil || live != nil {
		t.Fatalf("old token revived: %#v %v", live, err)
	}
	next, err := database.CreateAppRuntimeSession(ctx, user.ID, "journal", security.HashToken("restored"), space.ID, AppRuntimeSessionTTL)
	if err != nil {
		t.Fatal(err)
	}
	records, err := database.AppPersonalRecords(ctx, *next)
	if err != nil || len(records) != 1 {
		t.Fatalf("retained records: %#v %v", records, err)
	}
}
func TestPersonalSpaceTemplateCapturesOnlyApps(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	user, err := database.CreateUser("Template owner", "template-owner@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	space := createTestSpace(t, database, ctx, user.ID, "Family")
	if _, err = database.InstallSpaceApp(ctx, user.ID, space.ID, AppInstallSpec{ID: "journal", Version: "1.0.0", PermissionVersion: 1, Scopes: []string{"notes.read"}}, nil); err != nil {
		t.Fatal(err)
	}
	template, err := database.SavePersonalSpaceTemplate(ctx, user.ID, "", space.ID, "My family", "Tools only")
	if err != nil {
		t.Fatal(err)
	}
	if len(template.Apps) != 1 || template.Apps[0].AppID != "journal" {
		t.Fatalf("template: %#v", template)
	}
	if _, err = database.RemoveSpaceApp(ctx, user.ID, space.ID, "journal"); err != nil {
		t.Fatal(err)
	}
	saved, err := database.PersonalSpaceTemplates(ctx, user.ID)
	if err != nil || len(saved) != 1 || len(saved[0].Apps) != 1 {
		t.Fatalf("saved template changed: %#v %v", saved, err)
	}
}

func TestSpaceAppsMembersAndDelegatedManagement(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("Owner", "environment-owner@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	member, err := database.CreateUser("Member", "environment-member@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	space := createTestSpace(t, database, ctx, owner.ID, "Family")
	invite, err := database.InviteToSpace(ctx, owner.ID, space.ID, member.Email)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = database.RespondToSpaceInvite(ctx, member.ID, invite.ID, true); err != nil {
		t.Fatal(err)
	}
	spec := AppInstallSpec{ID: "journal", Version: "1.0.0", PermissionVersion: 1, Scopes: []string{"storage.read", "storage.write"}}
	if _, err = database.InstallSpaceApp(ctx, member.ID, space.ID, spec, nil); err == nil {
		t.Fatal("ordinary member installed app")
	}
	if _, err = database.InstallSpaceApp(ctx, owner.ID, space.ID, spec, nil); err != nil {
		t.Fatal(err)
	}
	apps, err := database.SpaceApps(ctx, member.ID, space.ID)
	if err != nil || len(apps) != 1 {
		t.Fatalf("member apps: %v %v", apps, err)
	}
	ownerSession, err := database.CreateAppRuntimeSession(ctx, owner.ID, "journal", security.HashToken("owner-record"), space.ID, AppRuntimeSessionTTL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = database.PutAppPersonalRecord(ctx, *ownerSession, "private", []byte(`{"secret":"owner only"}`)); err != nil {
		t.Fatal(err)
	}
	memberSession, err := database.CreateAppRuntimeSession(ctx, member.ID, "journal", security.HashToken("member-record"), space.ID, AppRuntimeSessionTTL)
	if err != nil {
		t.Fatal(err)
	}
	records, err := database.AppPersonalRecords(ctx, *memberSession)
	if err != nil || len(records) != 0 {
		t.Fatalf("private records leaked: %v %v", records, err)
	}
	note, err := database.CreateSpaceNote(ctx, owner.ID, space.ID, "Family notes")
	if err != nil {
		t.Fatal(err)
	}
	notes, err := database.AccessibleSpaceNotes(ctx, member.ID, space.ID)
	if err != nil || len(notes) != 1 || notes[0].ID != note.ID {
		t.Fatalf("shared note unavailable: %v %v", notes, err)
	}
	if _, err = database.RemoveSpaceApp(ctx, member.ID, space.ID, "journal"); err == nil {
		t.Fatal("ordinary member removed app")
	}
	if err = database.SetSpaceMemberPermission(ctx, owner.ID, space.ID, member.ID, PermissionAppsManage, "allow"); err != nil {
		t.Fatal(err)
	}
	if _, err = database.RemoveSpaceApp(ctx, member.ID, space.ID, "journal"); err != nil {
		t.Fatal(err)
	}
	if _, err = database.AccessibleSpaceNotes(ctx, owner.ID, space.ID); !errors.Is(err, ErrAppNotInstalled) {
		t.Fatalf("removed notes listing: %v", err)
	}
	if _, err = database.CreateSpaceNote(ctx, owner.ID, space.ID, "blocked"); !errors.Is(err, ErrAppNotInstalled) {
		t.Fatalf("removed note creation: %v", err)
	}
	if _, err = database.AppPersonalRecords(ctx, *ownerSession); !errors.Is(err, ErrAppRuntimeForbidden) {
		t.Fatalf("stale session records: %v", err)
	}
	if _, err = database.InstallSpaceApp(ctx, member.ID, space.ID, spec, nil); err != nil {
		t.Fatal(err)
	}
	notes, err = database.AccessibleSpaceNotes(ctx, member.ID, space.ID)
	if err != nil || len(notes) != 1 || notes[0].ID != note.ID {
		t.Fatalf("retained note lost: %v %v", notes, err)
	}
}

func TestSpaceAppsTemplateSelectionAndRetry(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("Template creator", "selected-template@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	apps := []AppInstallSpec{{ID: "journal", Version: "1.0.0", PermissionVersion: 1, Scopes: []string{}}}
	created, err := database.CreateSpaceWithTemplateIdempotent(ctx, owner.ID, "Family", "family", nil, "same-request", apps...)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := database.CreateSpaceWithTemplateIdempotent(ctx, owner.ID, "Family", "family", nil, "same-request", apps...)
	if err != nil || retry.Space.ID != created.Space.ID {
		t.Fatalf("retry duplicated space: %v %v", retry, err)
	}
	installed, err := database.SpaceApps(ctx, owner.ID, created.Space.ID)
	if err != nil || len(installed) != 1 || installed[0].AppID != "journal" {
		t.Fatalf("unreviewed apps: %v %v", installed, err)
	}
	notes, err := database.AccessibleSpaceNotes(ctx, owner.ID, created.Space.ID)
	if err != nil || len(notes) != 1 || notes[0].TitleProjection != "Family notes" {
		t.Fatalf("duplicate/wrong seeds: %v %v", notes, err)
	}
	if _, err = database.CreateSpaceWithTemplateIdempotent(ctx, owner.ID, "Family", "family", nil, "same-request"); !errors.Is(err, ErrSpaceConflict) {
		t.Fatalf("changed retry accepted: %v", err)
	}
}

func TestSpaceAppsConnectionsRequirePersonalSelection(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("Connection owner", "space-connection-owner@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	member, err := database.CreateUser("Connection member", "space-connection-member@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	family := createTestSpace(t, database, ctx, owner.ID, "Family")
	work := createTestSpace(t, database, ctx, owner.ID, "Work")
	spec := AppInstallSpec{ID: "inbox", Version: "1", PermissionVersion: 1, Scopes: []string{"connections.read", "mail.read"}}
	for _, space := range []string{family.ID, work.ID} {
		if _, err = database.InstallSpaceApp(ctx, owner.ID, space, spec, nil); err != nil {
			t.Fatal(err)
		}
	}
	invite, err := database.InviteToSpace(ctx, owner.ID, family.ID, member.Email)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = database.RespondToSpaceInvite(ctx, member.ID, invite.ID, true); err != nil {
		t.Fatal(err)
	}
	account, err := database.SaveConnectedAccount(ctx, ConnectedAccount{UserID: owner.ID, Provider: "google", AccountID: "private-owner", AccountDisplay: "owner@example.com", CredentialCiphertext: []byte("private-token"), CredentialNonce: []byte("nonce"), KeyVersion: 1, Capabilities: []string{"mail"}, GrantedScopes: []string{"gmail.readonly"}})
	if err != nil {
		t.Fatal(err)
	}
	newContext := func(space string) context.Context {
		t.Helper()
		session, e := database.CreateAppRuntimeSession(ctx, owner.ID, "inbox", security.HashToken("personal-selection-"+space), space, AppRuntimeSessionTTL)
		if e != nil {
			t.Fatal(e)
		}
		return WithAppExecutionAuthority(ctx, *session)
	}
	familyCtx := newContext(family.ID)
	workCtx := newContext(work.ID)
	accounts, err := database.ConnectedAccounts(familyCtx, owner.ID)
	if err != nil || len(accounts) != 0 {
		t.Fatalf("unselected accounts exposed: %v %v", accounts, err)
	}
	if err = database.SetSpaceAppConnections(ctx, member.ID, family.ID, "inbox", []string{account.ID}); !errors.Is(err, ErrAppRuntimeForbidden) {
		t.Fatalf("member selected another person's login: %v", err)
	}
	if err = database.SetSpaceAppConnections(ctx, owner.ID, family.ID, "inbox", []string{account.ID}); err != nil {
		t.Fatal(err)
	}
	accounts, err = database.ConnectedAccounts(familyCtx, owner.ID)
	if err != nil || len(accounts) != 1 {
		t.Fatalf("selected account unavailable: %v %v", accounts, err)
	}
	if _, err = database.ConnectedAccount(workCtx, owner.ID, account.ID); !errors.Is(err, ErrSpaceNotFound) {
		t.Fatalf("account exposed in another Space: %v", err)
	}
	if err = database.SetSpaceAppConnections(ctx, owner.ID, family.ID, "inbox", nil); err != nil {
		t.Fatal(err)
	}
	if _, err = database.ConnectedAccount(familyCtx, owner.ID, account.ID); !errors.Is(err, ErrSpaceNotFound) {
		t.Fatalf("removed selection remains accessible: %v", err)
	}
	if _, err = database.ConnectedAccount(ctx, owner.ID, account.ID); err != nil {
		t.Fatalf("credential destroyed: %v", err)
	}
}

func TestSpaceAppsDependenciesAndSharedOrder(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("App order owner", "space-app-order@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	space := createTestSpace(t, database, ctx, owner.ID, "Environment")
	child := AppInstallSpec{ID: "companion", Version: "1", PermissionVersion: 1, Scopes: []string{}}
	metadata := []byte(`{"requires_apps":["chat"]}`)
	if _, err = database.InstallSpaceApp(ctx, owner.ID, space.ID, child, metadata); !errors.Is(err, ErrAppDependencies) {
		t.Fatalf("missing dependency accepted: %v", err)
	}
	if _, err = database.InstallSpaceApp(ctx, owner.ID, space.ID, AppInstallSpec{ID: "chat", Version: "1", PermissionVersion: 1, Scopes: []string{}}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err = database.InstallSpaceApp(ctx, owner.ID, space.ID, child, metadata); err != nil {
		t.Fatal(err)
	}
	if err = database.ReorderSpaceApps(ctx, owner.ID, space.ID, []string{"companion", "chat"}); err != nil {
		t.Fatal(err)
	}
	apps, err := database.SpaceApps(ctx, owner.ID, space.ID)
	if err != nil || len(apps) != 2 || apps[0].AppID != "companion" {
		t.Fatalf("shared order: %v %v", apps, err)
	}
	if err = database.ReorderSpaceApps(ctx, owner.ID, space.ID, []string{"chat", "chat"}); err == nil {
		t.Fatal("duplicate app order accepted")
	}
	if _, err = database.RemoveSpaceApp(ctx, owner.ID, space.ID, "chat"); !errors.Is(err, ErrAppDependencies) {
		t.Fatalf("dependent app stranded: %v", err)
	}
	if _, err = database.RemoveSpaceApp(ctx, owner.ID, space.ID, "companion"); err != nil {
		t.Fatal(err)
	}
	if _, err = database.RemoveSpaceApp(ctx, owner.ID, space.ID, "chat"); err != nil {
		t.Fatal(err)
	}
}
