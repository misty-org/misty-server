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
// fresh device-key signature open it; presence and LAN candidates flow only
// between added devices; a connect intent sends each device the other's
// candidates; nothing but LAN addresses is relayed.

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

func TestDeviceChannelRoutesPresenceAndCandidatesOnlyBetweenAddedDevices(t *testing.T) {
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
	b.next("ready")
	p, err := f.openChannel(server, pending, pending.key)
	if err != nil {
		t.Fatal(err)
	}
	if ready := p.next("ready"); ready["state"] != "pending" || ready["presence"] != nil {
		t.Fatalf("a pending device saw presence: %v", ready)
	}

	// B's addresses: only LAN candidates survive, and they are not broadcast.
	if err := b.conn.WriteJSON(map[string]any{"type": "address", "candidates": []string{"192.168.1.20:4040", "8.8.8.8:53", "127.0.0.1:9"}}); err != nil {
		t.Fatal(err)
	}
	delta := a.next("presence.delta")
	if device, _ := delta["device"].(map[string]any); device["deviceId"] != second.id || device["online"] != true || device["candidates"] != nil {
		t.Fatalf("presence delta = %v", delta)
	}
	// A pending device cannot publish addresses or ask for an introduction.
	if err := p.conn.WriteJSON(map[string]any{"type": "connect", "deviceId": first.id}); err != nil {
		t.Fatal(err)
	}
	if frame := p.next("error"); frame["code"] != "device_not_added" {
		t.Fatalf("pending connect = %v", frame)
	}

	// A wants B: each receives the other's candidates.
	if err := a.conn.WriteJSON(map[string]any{"type": "address", "candidates": []string{"10.0.0.7:5050"}}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if err := a.conn.WriteJSON(map[string]any{"type": "connect", "deviceId": second.id}); err != nil {
		t.Fatal(err)
	}
	toA := a.next("candidates")
	if toA["deviceId"] != second.id || len(toA["addresses"].([]any)) != 1 || toA["addresses"].([]any)[0] != "192.168.1.20:4040" {
		t.Fatalf("candidates to A = %v", toA)
	}
	toB := b.next("candidates")
	if toB["deviceId"] != first.id || toB["addresses"].([]any)[0] != "10.0.0.7:5050" {
		t.Fatalf("candidates to B = %v", toB)
	}
	// A device that is offline is reported as unreachable.
	_ = b.conn.Close()
	time.Sleep(200 * time.Millisecond)
	if err := a.conn.WriteJSON(map[string]any{"type": "connect", "deviceId": second.id}); err != nil {
		t.Fatal(err)
	}
	if frame := a.next("unreachable"); frame["deviceId"] != second.id {
		t.Fatalf("offline device = %v", frame)
	}

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
