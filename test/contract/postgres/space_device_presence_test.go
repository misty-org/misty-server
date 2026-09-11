package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	. "github.com/kannachi323/misty/server/internal/platform/postgres"
	"strings"
	"testing"
	"time"
)

func TestSpacePeerPresenceRequiresCurrentAppAndPreservesPersonalPairing(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	user, err := database.CreateUser("Peer owner", "space-peer-owner@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	family := createTestSpace(t, database, ctx, user.ID, "Family")
	work := createTestSpace(t, database, ctx, user.ID, "Work")
	spec := AppInstallSpec{ID: "files", Version: "1.0.0", PermissionVersion: 1, Scopes: []string{"files.read", "connections.read"}}
	installed, err := database.InstallSpaceApp(ctx, user.ID, family.ID, spec, nil)
	if err != nil {
		t.Fatal(err)
	}
	other, err := database.InstallSpaceApp(ctx, user.ID, work.ID, spec, nil)
	if err != nil {
		t.Fatal(err)
	}
	device := func(name, key, endpoint string) *TrustedDevice {
		t.Helper()
		d, err := database.RegisterTrustedDevice(user.ID, name, strings.Repeat(key, 64), "macos", strings.Repeat(endpoint, 64), json.RawMessage(`["misty-device/1","misty-device/2"]`), nil)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	first := device("First", "a", "a")
	second := device("Second", "b", "b")
	pairing, err := database.CreateDevicePairingSession(user.ID, first.ID, strings.Repeat("a", 64), strings.Repeat("b", 64), time.Now().Add(4*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = database.RedeemDevicePairingSession(user.ID, second.ID, pairing.ID, strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	pair, err := database.ConfirmDevicePairing(user.ID, first.ID, pairing.ID)
	if err != nil {
		t.Fatal(err)
	}
	presence := func(app *SpaceAppInstallation, id string) SpaceDevicePresence {
		return SpaceDevicePresence{SpaceID: app.SpaceID, InstalledVersion: app.InstalledVersion, AuthorityGeneration: app.AuthorityGeneration, EndpointID: strings.Repeat(id, 64), Addressing: json.RawMessage(`{"id":"test"}`), ProtocolVersion: SpacePeerProtocol, ConnectionHint: "direct"}
	}
	familyFirst, familySecond := presence(installed, "c"), presence(installed, "d")
	workFirst, workSecond := presence(other, "e"), presence(other, "f")
	update := func(deviceID string, p SpaceDevicePresence) {
		t.Helper()
		if err := database.UpdateSpaceDevicePresence(ctx, user.ID, deviceID, p); err != nil {
			t.Fatal(err)
		}
	}
	// Existing device-wide presence is never used as Space authority.
	if err = database.UpdateDevicePresence(user.ID, first.ID, first.P2PEndpointID, "misty-device/1", "direct", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	if _, err = database.SpacePeerTicketSubject(ctx, user.ID, family.ID, first.ID, second.ID); !errors.Is(err, ErrDevicePair) {
		t.Fatalf("legacy presence authorized Space: %v", err)
	}
	update(first.ID, familyFirst)
	update(second.ID, familySecond)
	update(first.ID, workFirst)
	update(second.ID, workSecond)
	subject, err := database.SpacePeerTicketSubject(ctx, user.ID, family.ID, first.ID, second.ID)
	if err != nil {
		t.Fatal(err)
	}
	if subject.PairID != pair.ID || subject.SpaceID != family.ID || subject.SourceEndpointID != familyFirst.EndpointID || subject.TargetEndpointID != familySecond.EndpointID || subject.AuthorityGeneration != installed.AuthorityGeneration {
		t.Fatalf("wrong Space ticket subject: %#v", subject)
	}
	stranger, err := database.CreateUser("Member", "space-peer-member@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	if err = database.UpdateSpaceDevicePresence(ctx, stranger.ID, first.ID, familyFirst); !errors.Is(err, ErrSpaceForbidden) {
		t.Fatalf("nonmember presence: %v", err)
	}
	if err = database.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO space_members(space_id,user_id,role) VALUES($1,$2,'member')`, family.ID, stranger.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err = database.UpdateSpaceDevicePresence(ctx, stranger.ID, first.ID, familyFirst); !errors.Is(err, ErrDeviceNotFound) {
		t.Fatalf("member borrowed personal device: %v", err)
	}
	if _, err = database.ConnectedSpacePeers(ctx, stranger.ID, family.ID, first.ID); !errors.Is(err, ErrDeviceNotFound) {
		t.Fatalf("member saw personal peers: %v", err)
	}
	if _, err = database.RemoveSpaceApp(ctx, user.ID, family.ID, "files"); err != nil {
		t.Fatal(err)
	}
	if _, err = database.SpacePeerTicketSubject(ctx, user.ID, family.ID, first.ID, second.ID); !errors.Is(err, ErrAppRuntimeForbidden) {
		t.Fatalf("removed Files authorized peers: %v", err)
	}
	if _, err = database.SpacePeerTicketSubject(ctx, user.ID, work.ID, first.ID, second.ID); err != nil {
		t.Fatalf("Family removal broke Work: %v", err)
	}
	if _, err = database.PeerTicketSubject(user.ID, first.ID, second.ID); err != nil {
		t.Fatalf("personal pairing or identity was lost: %v", err)
	}
	restored, err := database.InstallSpaceApp(ctx, user.ID, family.ID, spec, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = database.UpdateSpaceDevicePresence(ctx, user.ID, first.ID, familyFirst); !errors.Is(err, ErrAppRuntimeForbidden) {
		t.Fatalf("stale generation restored presence: %v", err)
	}
	if _, err = database.SpacePeerTicketSubject(ctx, user.ID, family.ID, first.ID, second.ID); !errors.Is(err, ErrDevicePair) {
		t.Fatalf("restoration revived old presence: %v", err)
	}
	update(first.ID, presence(restored, "c"))
	update(second.ID, presence(restored, "d"))
	if _, err = database.SpacePeerTicketSubject(ctx, user.ID, family.ID, first.ID, second.ID); err != nil {
		t.Fatal(err)
	}
	spec.Scopes = []string{"files.read"}
	if _, err = database.InstallSpaceApp(ctx, user.ID, family.ID, spec, nil); err != nil {
		t.Fatal(err)
	}
	if _, err = database.SpacePeerTicketSubject(ctx, user.ID, family.ID, first.ID, second.ID); !errors.Is(err, ErrAppRuntimeForbidden) {
		t.Fatalf("reduced permission authorized peers: %v", err)
	}
	if err = database.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE space_device_presence SET last_heartbeat_at=NOW()-INTERVAL '2 minutes' WHERE space_id=$1`, work.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = database.SpacePeerTicketSubject(ctx, user.ID, work.ID, first.ID, second.ID); !errors.Is(err, ErrDevicePair) {
		t.Fatalf("expired presence issued ticket: %v", err)
	}
	peers, err := database.ConnectedSpacePeers(ctx, user.ID, work.ID, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(peers) != 1 || peers[0].P2PEndpointID != "" || peers[0].LastHeartbeatAt != nil {
		t.Fatalf("expired presence exposed endpoints: %#v", peers)
	}
}
