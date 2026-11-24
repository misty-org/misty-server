package db

import (
	"encoding/json"
	"errors"
	"testing"

	. "github.com/misty-org/misty-server/internal/platform/db"
	"github.com/misty-org/misty-server/internal/spaces"
)

func newIntegrationToken(t *testing.T) (string, string) {
	t.Helper()
	token, hash, err := spaces.NewIntegrationToken()
	if err != nil {
		t.Fatal(err)
	}
	return token, hash
}

func TestSpaceIntegrationsAreManagedByTheOwnerOrTheirPermission(t *testing.T) {
	f := newRolesFixture(t, "integrations")
	_, hash := newIntegrationToken(t)
	input := SpaceIntegrationInput{Name: "Deploy bot", Avatar: json.RawMessage(`{"emoji":"🤖"}`), Access: []string{"rooms.write", "rooms.read"}, KeyPrefix: "app:board:"}
	if _, err := f.database.CreateSpaceIntegration(f.ctx, f.crew.ID, f.space.ID, input, hash); !errors.Is(err, ErrSpaceForbidden) {
		t.Fatalf("a member without integrations.manage made one: %v", err)
	}
	created, err := f.database.CreateSpaceIntegration(f.ctx, f.owner.ID, f.space.ID, input, hash)
	if err != nil {
		t.Fatalf("CreateSpaceIntegration(owner) error = %v", err)
	}
	if !spaces.ValidIntegrationID(created.ID) || created.Name != "Deploy bot" || created.KeyPrefix != "app:board:" ||
		len(created.Access) != 2 || created.Access[0] != "rooms.read" || created.CreatedBy != f.owner.ID || created.LastUsedAt != nil {
		t.Fatalf("created = %#v", created)
	}
	bots := f.role(t, "Bots", spaces.PermissionIntegrationsManage)
	if _, err := f.database.Conn.ExecContext(f.ctx, `INSERT INTO space_member_roles(space_id,user_id,role_id) VALUES($1,$2,$3)`,
		f.space.ID, f.crew.ID, bots.ID); err != nil {
		t.Fatal(err)
	}
	name := "Release bot"
	empty := ""
	updated, err := f.database.UpdateSpaceIntegration(f.ctx, f.crew.ID, f.space.ID, created.ID,
		SpaceIntegrationPatch{Name: &name, Access: []string{"rooms.read"}, SetAccess: true, KeyPrefix: &empty})
	if err != nil || updated.Name != "Release bot" || len(updated.Access) != 1 || updated.KeyPrefix != "" {
		t.Fatalf("UpdateSpaceIntegration() = %#v, %v", updated, err)
	}
	if _, err := f.database.UpdateSpaceIntegration(f.ctx, f.crew.ID, f.space.ID, created.ID,
		SpaceIntegrationPatch{Access: []string{"administrator"}, SetAccess: true}); !errors.Is(err, ErrSpaceInvalid) {
		t.Fatalf("an integration was given administrator: %v", err)
	}
	if _, err := f.database.SpaceIntegrations(f.ctx, f.rest.ID, f.space.ID); !errors.Is(err, ErrSpaceForbidden) {
		t.Fatalf("a plain member listed the managed integrations: %v", err)
	}
	listed, err := f.database.SpaceIntegrations(f.ctx, f.crew.ID, f.space.ID)
	if err != nil || len(listed) != 1 {
		t.Fatalf("SpaceIntegrations() = %#v, %v", listed, err)
	}
	members, err := f.database.SpaceMemberIntegrations(f.ctx, f.rest.ID, f.space.ID)
	if err != nil || len(members) != 1 || members[0].ID != created.ID || members[0].Name != "Release bot" {
		t.Fatalf("SpaceMemberIntegrations() = %#v, %v", members, err)
	}
	raw, err := json.Marshal(members[0])
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields) != 5 || fields["key_prefix"] != "" {
		t.Fatalf("member view = %s, want id, name, avatar, access and key_prefix", raw)
	}
}

func TestSpaceIntegrationTokensExchangeReplaceAndEnd(t *testing.T) {
	f := newRolesFixture(t, "integration-tokens")
	_, hash := newIntegrationToken(t)
	created, err := f.database.CreateSpaceIntegration(f.ctx, f.owner.ID, f.space.ID,
		SpaceIntegrationInput{Name: "Webhook", Access: []string{"rooms.read"}}, hash)
	if err != nil {
		t.Fatal(err)
	}
	found, err := f.database.SpaceIntegrationByToken(f.ctx, hash)
	if err != nil || found.ID != created.ID || found.SpaceID != f.space.ID || found.LastUsedAt == nil {
		t.Fatalf("SpaceIntegrationByToken() = %#v, %v", found, err)
	}
	_, replacement := newIntegrationToken(t)
	if err := f.database.ReplaceSpaceIntegrationToken(f.ctx, f.owner.ID, f.space.ID, created.ID, replacement); err != nil {
		t.Fatal(err)
	}
	if _, err := f.database.SpaceIntegrationByToken(f.ctx, hash); !errors.Is(err, ErrSpaceNotFound) {
		t.Fatalf("the replaced token still works: %v", err)
	}
	if _, err := f.database.SpaceIntegrationByToken(f.ctx, replacement); err != nil {
		t.Fatalf("the new token fails: %v", err)
	}
	if err := f.database.RemoveSpaceIntegration(f.ctx, f.owner.ID, f.space.ID, created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.database.SpaceIntegrationByToken(f.ctx, replacement); !errors.Is(err, ErrSpaceNotFound) {
		t.Fatalf("a removed integration still exchanges: %v", err)
	}
	if err := f.database.RemoveSpaceIntegration(f.ctx, f.owner.ID, f.space.ID, created.ID); !errors.Is(err, ErrSpaceNotFound) {
		t.Fatalf("removing twice: %v", err)
	}
}

func TestSpaceIntegrationsStayOutOfPersonalAndUnderTheLimit(t *testing.T) {
	f := newRolesFixture(t, "integration-limits")
	personal := personalSpace(t, f.database, f.ctx, f.owner.ID)
	_, hash := newIntegrationToken(t)
	if _, err := f.database.CreateSpaceIntegration(f.ctx, f.owner.ID, personal.ID,
		SpaceIntegrationInput{Name: "Mine", Access: []string{"rooms.read"}}, hash); !errors.Is(err, ErrSpaceNotShareable) {
		t.Fatalf("an integration was made in Personal: %v", err)
	}
	one := int64(1)
	if err := f.database.SetAccountLimits(f.ctx, f.owner.ID, AccountLimits{IntegrationsPerSpace: &one}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.database.CreateSpaceIntegration(f.ctx, f.owner.ID, f.space.ID,
		SpaceIntegrationInput{Name: "First", Access: []string{"rooms.read"}}, hash); err != nil {
		t.Fatal(err)
	}
	_, second := newIntegrationToken(t)
	if _, err := f.database.CreateSpaceIntegration(f.ctx, f.owner.ID, f.space.ID,
		SpaceIntegrationInput{Name: "Second", Access: []string{"rooms.read"}}, second); !errors.Is(err, ErrSpaceIntegrationLimit) {
		t.Fatalf("the limit let a second integration in: %v", err)
	}
	limits, err := f.database.AccountLimitsFor(f.ctx, f.owner.ID)
	if err != nil || limits.IntegrationsPerSpace == nil || *limits.IntegrationsPerSpace != 1 {
		t.Fatalf("AccountLimitsFor() = %#v, %v", limits, err)
	}
}
