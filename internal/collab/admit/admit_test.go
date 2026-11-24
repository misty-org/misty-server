package admit

import (
	"context"
	"testing"

	"github.com/misty-org/misty-server/internal/collab/wire"
)

func TestIntegrationsGetRoomsOnly(t *testing.T) {
	gate := Gate{SpaceExists: func(context.Context, string) (bool, error) { return true, nil }}
	who := Who{Kind: KindIntegration, ID: "int_1", Integration: IntegrationToken{ID: "int_1", SpaceID: "sp", Access: []string{AccessRoomsRead}, KeyPrefix: "bot:"}}
	if grant, err := gate.Check(context.Background(), who, "sp", "bot:board", ""); err != nil || grant.Role != RoleView {
		t.Fatalf("read-only integration %+v %v", grant, err)
	}
	for _, c := range []struct{ space, key, file string }{{"other", "bot:board", ""}, {"sp", "board", ""}, {"sp", "bot:board", "drv_a"}} {
		if _, err := gate.Check(context.Background(), who, c.space, c.key, c.file); CodeOf(err) != wire.CloseForbidden {
			t.Errorf("%+v gave %v", c, err)
		}
	}
	who.Integration.Access = []string{"other"}
	if _, err := gate.Check(context.Background(), who, "sp", "bot:board", ""); CodeOf(err) != wire.CloseForbidden {
		t.Fatalf("an integration without room access got in: %v", err)
	}
}

func TestPeopleAndFiles(t *testing.T) {
	gate := Gate{
		Member: func(_ context.Context, spaceID, userID string) (Member, error) {
			if spaceID != "sp" {
				return Member{}, ErrSpaceMissing
			}
			if userID != "ana" {
				return Member{}, ErrNotMember
			}
			return Member{Name: "Ana"}, nil
		},
		File: func(_ context.Context, _, itemID, _ string) (FileAccess, error) {
			switch itemID {
			case "drv_edit":
				return FileAccess{Read: true, Write: true}, nil
			case "drv_view":
				return FileAccess{Read: true}, nil
			}
			return FileAccess{}, ErrFileMissing
		},
	}
	ana := Who{Kind: KindPerson, ID: "ana"}
	if grant, err := gate.Check(context.Background(), ana, "sp", "k", ""); err != nil || grant.Role != RoleEdit || grant.Who.Identity.Name != "Ana" {
		t.Fatalf("member %+v %v", grant, err)
	}
	if grant, _ := gate.Check(context.Background(), ana, "sp", "k", "drv_view"); grant.Role != RoleView {
		t.Fatalf("view file %+v", grant)
	}
	cases := map[string]int{"missing-space": wire.CloseNotFound, "not-member": wire.CloseForbidden, "missing-file": wire.CloseNotFound}
	for name, want := range cases {
		who, space, file := ana, "sp", ""
		switch name {
		case "missing-space":
			space = "gone"
		case "not-member":
			who = Who{Kind: KindPerson, ID: "sam"}
		case "missing-file":
			file = "drv_gone"
		}
		if _, err := gate.Check(context.Background(), who, space, "k", file); CodeOf(err) != want {
			t.Errorf("%s gave %v", name, err)
		}
	}
	if _, err := gate.Authenticate(""); CodeOf(err) != wire.CloseUnauthorized {
		t.Fatalf("an empty token: %v", err)
	}
}
