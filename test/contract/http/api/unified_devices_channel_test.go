package api

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	. "github.com/kannachi323/misty/server/internal/platform/httpapi"
)

// The device channel end to end over real WebSockets: a one-use ticket and a
// fresh device-key signature open it; presence flows only between added
// devices; once authenticated a device only listens, so every frame it sends
// is refused.

type channelClient struct {
	t    *testing.T
	conn *websocket.Conn
}

func (f *deviceFixture) channelServer() *httptest.Server {
	service := NewAgentsService(f.database)
	f.router.Post("/devices/{deviceID}/channel-ticket", service.DeviceChannelTicket())
	f.router.Get("/devices/channel", service.DeviceChannel())
	server := httptest.NewServer(f.router)
	f.t.Cleanup(server.Close)
	return server
}

func (f *deviceFixture) openChannel(server *httptest.Server, device testDevice, signer ed25519.PrivateKey) (*channelClient, error) {
	ticket := performConversationRequest(f.t, f.router, http.MethodPost, "/devices/"+device.id+"/channel-ticket", f.token, map[string]any{})
	if ticket.Code != http.StatusCreated {
		f.t.Fatalf("ticket: %d %s", ticket.Code, ticket.Body.String())
	}
	var body struct {
		Ticket string `json:"ticket"`
	}
	_ = json.Unmarshal(ticket.Body.Bytes(), &body)
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/devices/channel?ticket="+body.Ticket, nil)
	if err != nil {
		return nil, err
	}
	client := &channelClient{t: f.t, conn: conn}
	f.t.Cleanup(func() { _ = conn.Close() })
	challenge := client.read()
	proof, _ := json.Marshal([]any{"misty.device.channel.v1", f.userID, device.id, challenge["instance"], challenge["challenge"]})
	if err := conn.WriteJSON(map[string]any{"type": "authenticate", "signature": base64.StdEncoding.EncodeToString(ed25519.Sign(signer, proof))}); err != nil {
		return nil, err
	}
	return client, nil
}

func (c *channelClient) read() map[string]any {
	c.t.Helper()
	_ = c.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var frame map[string]any
	if err := c.conn.ReadJSON(&frame); err != nil {
		c.t.Fatalf("read frame: %v", err)
	}
	return frame
}

// next skips account-event hints until a frame of this type arrives.
func (c *channelClient) next(kind string) map[string]any {
	c.t.Helper()
	for range 20 {
		if frame := c.read(); frame["type"] == kind {
			return frame
		}
	}
	c.t.Fatalf("no %s frame", kind)
	return nil
}

// presenceEntry checks a presence entry is exactly a device id and whether it
// is online: no addresses, endpoints or network hints.
func presenceEntry(t *testing.T, value any, deviceID string, online bool) {
	t.Helper()
	entry, _ := value.(map[string]any)
	if len(entry) != 2 || entry["deviceId"] != deviceID || entry["online"] != online {
		t.Fatalf("presence entry = %v, want %s online=%v", value, deviceID, online)
	}
}

func TestDeviceChannelRoutesPresenceOnlyBetweenAddedDevices(t *testing.T) {
	f := newDeviceFixture(t)
	first := f.register("First")
	second := f.register("Second")
	pending := f.register("Pending")
	if response := f.admit(first, 1, []testDevice{first}, f.root); response.Code != http.StatusOK {
		t.Fatalf("admit: %d", response.Code)
	}
	if response := f.admit(second, 2, []testDevice{first, second}, f.root); response.Code != http.StatusOK {
		t.Fatalf("admit: %d", response.Code)
	}
	server := f.channelServer()

	// A signature from any other key closes the socket.
	intruder, err := f.openChannel(server, first, second.key)
	if err != nil {
		t.Fatal(err)
	}
	_ = intruder.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, _, err := intruder.conn.ReadMessage(); err == nil {
		t.Fatal("a channel opened with another device's key")
	}

	a, err := f.openChannel(server, first, first.key)
	if err != nil {
		t.Fatal(err)
	}
	if ready := a.next("ready"); ready["state"] != "admitted" {
		t.Fatalf("ready = %v", ready)
	}
	b, err := f.openChannel(server, second, second.key)
	if err != nil {
		t.Fatal(err)
	}
	ready := b.next("ready")
	if presence, _ := ready["presence"].([]any); len(presence) != 2 {
		t.Fatalf("ready presence = %v", ready["presence"])
	} else {
		presenceEntry(t, presence[0], min(first.id, second.id), true)
		presenceEntry(t, presence[1], max(first.id, second.id), true)
	}
	// A learns B came online.
	presenceEntry(t, a.next("presence.delta")["device"], second.id, true)

	p, err := f.openChannel(server, pending, pending.key)
	if err != nil {
		t.Fatal(err)
	}
	if ready := p.next("ready"); ready["state"] != "pending" || ready["presence"] != nil {
		t.Fatalf("a pending device saw presence: %v", ready)
	}

	// Devices only listen: LAN addresses, connect intents and any other frame
	// are refused, from added and pending devices alike.
	for _, frame := range []map[string]any{
		{"type": "address", "candidates": []string{"192.168.1.20:4040"}},
		{"type": "connect", "deviceId": first.id},
		{"type": "authenticate", "signature": "x"},
		{"type": "ping"},
	} {
		if err := b.conn.WriteJSON(frame); err != nil {
			t.Fatal(err)
		}
		if reply := b.next("error"); reply["code"] != "invalid_device_frame" {
			t.Fatalf("%v answered %v", frame["type"], reply)
		}
	}
	if err := p.conn.WriteJSON(map[string]any{"type": "connect", "deviceId": first.id}); err != nil {
		t.Fatal(err)
	}
	if reply := p.next("error"); reply["code"] != "invalid_device_frame" {
		t.Fatalf("pending connect = %v", reply)
	}

	// B going away is presence too.
	_ = b.conn.Close()
	presenceEntry(t, a.next("presence.delta")["device"], second.id, false)

	// Removing a device closes its socket.
	now := time.Now().Unix()
	removal := signedRecord(f.root, "misty.device.list.v1", f.userID, f.vaultID, 3, 1, pairs(first), pairs(second), now)
	b2, err := f.openChannel(server, second, second.key)
	if err == nil {
		b2.next("ready")
		if response := f.signed(first, http.MethodPost, "/devices/"+first.id+"/remove-device", map[string]any{"targetDeviceId": second.id, "list": removal}); response.Code != http.StatusOK {
			t.Fatalf("remove: %d", response.Code)
		}
		if frame := b2.next("revoked"); frame["type"] != "revoked" {
			t.Fatalf("removed device was not told: %v", frame)
		}
	}
}
