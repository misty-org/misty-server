package db

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestFilesPeersRequireOwnedPairedFreshDevicesWithoutAppInstallation(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	user, err := database.CreateUser("Peer owner", "files-peer-owner@example.invalid", "password123")
	if err != nil {
		t.Fatal(err)
	}
	stranger, err := database.CreateUser("Other owner", "files-peer-other@example.invalid", "password123")
	if err != nil {
		t.Fatal(err)
	}
	device := func(name, key string) *TrustedDevice {
		t.Helper()
		d, err := database.RegisterTrustedDevice(user.ID, name, strings.Repeat(key, 64), "macos", strings.Repeat(key, 64), json.RawMessage(`["misty-device/1","misty-device/2"]`), nil)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	first, second := device("First", "a"), device("Second", "b")
	presence := func(endpoint string) SpaceDevicePresence {
		return SpaceDevicePresence{SpaceID: PersonalAppDeviceScope, InstalledVersion: FilesPeerVersion, AuthorityGeneration: FilesPeerGeneration, EndpointID: strings.Repeat(endpoint, 64), Addressing: json.RawMessage(`{"id":"test"}`), ProtocolVersion: SpacePeerProtocol, ConnectionHint: "direct"}
	}
	for _, d := range []*TrustedDevice{first, second} {
		if err := database.UpdateSpaceDevicePresence(ctx, user.ID, d.ID, presence("c")); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := database.SpacePeerTicketSubject(ctx, user.ID, PersonalAppDeviceScope, first.ID, second.ID); !errors.Is(err, ErrDevicePair) {
		t.Fatalf("unpaired endpoints admitted: %v", err)
	}
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
	subject, err := database.SpacePeerTicketSubject(ctx, user.ID, PersonalAppDeviceScope, first.ID, second.ID)
	if err != nil || subject.PairID != pair.ID || subject.InstalledVersion != FilesPeerVersion {
		t.Fatalf("first-party ticket: %#v %v", subject, err)
	}
	if err := database.UpdateSpaceDevicePresence(ctx, stranger.ID, first.ID, presence("d")); !errors.Is(err, ErrDeviceNotFound) {
		t.Fatalf("borrowed device: %v", err)
	}
	if _, err := database.ConnectedSpacePeers(ctx, stranger.ID, PersonalAppDeviceScope, first.ID); !errors.Is(err, ErrDeviceNotFound) {
		t.Fatalf("cross-account peer list: %v", err)
	}
	if _, err := database.SpacePeerTicketSubject(ctx, stranger.ID, PersonalAppDeviceScope, first.ID, second.ID); !errors.Is(err, ErrDevicePair) {
		t.Fatalf("cross-account ticket: %v", err)
	}
	stale := presence("d")
	stale.AuthorityGeneration++
	if err := database.UpdateSpaceDevicePresence(ctx, user.ID, first.ID, stale); !errors.Is(err, ErrAppRuntimeForbidden) {
		t.Fatalf("unknown protocol generation: %v", err)
	}
	if _, err := database.Conn.ExecContext(ctx, `UPDATE space_device_presence SET last_heartbeat_at=NOW()-INTERVAL '2 minutes' WHERE owner_user_id=$1`, user.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.SpacePeerTicketSubject(ctx, user.ID, PersonalAppDeviceScope, first.ID, second.ID); !errors.Is(err, ErrDevicePair) {
		t.Fatalf("stale endpoints admitted: %v", err)
	}
	peers, err := database.ConnectedSpacePeers(ctx, user.ID, PersonalAppDeviceScope, first.ID)
	if err != nil || len(peers) != 1 || peers[0].P2PEndpointID != "" || peers[0].LastHeartbeatAt != nil {
		t.Fatalf("stale endpoints disclosed: %#v %v", peers, err)
	}
	for _, d := range []*TrustedDevice{first, second} {
		if err := database.UpdateSpaceDevicePresence(ctx, user.ID, d.ID, presence("e")); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := database.Conn.ExecContext(ctx, `UPDATE trusted_devices SET revoked_at=NOW() WHERE id=$1`, second.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.SpacePeerTicketSubject(ctx, user.ID, PersonalAppDeviceScope, first.ID, second.ID); !errors.Is(err, ErrDevicePair) {
		t.Fatalf("revoked endpoint admitted: %v", err)
	}
	if err := database.UpdateSpaceDevicePresence(ctx, user.ID, second.ID, presence("f")); !errors.Is(err, ErrDeviceNotFound) {
		t.Fatalf("revoked device published: %v", err)
	}
}
