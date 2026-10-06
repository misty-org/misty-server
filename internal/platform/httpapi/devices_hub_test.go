package api

import "testing"

func TestDeviceHubAcceptsOnlyLANCandidates(t *testing.T) {
	for _, value := range []string{"192.168.1.20:4040", "10.0.0.2:1", "172.16.4.4:9", "100.101.1.2:7", "[fd00::1]:5", "[fe80::1]:6"} {
		if _, _, ok := lanCandidate(value); !ok {
			t.Fatalf("%s should be a LAN candidate", value)
		}
	}
	for _, value := range []string{"8.8.8.8:53", "127.0.0.1:80", "[2001:db8::1]:1", "0.0.0.0:1", "224.0.0.1:5", "192.168.1.2:0", "example.com:1", "192.168.1.2"} {
		if _, _, ok := lanCandidate(value); ok {
			t.Fatalf("%s must not be dialed", value)
		}
	}
	if _, overlay, _ := lanCandidate("100.64.0.9:1"); !overlay {
		t.Fatal("shared address space marks an overlay network")
	}
}

func TestDeviceHubNetworkKeysCompareWithoutRevealing(t *testing.T) {
	hub := newDeviceHub(nil)
	if hub.networkKey("203.0.113.5") != hub.networkKey("203.0.113.5") || hub.networkKey("203.0.113.5") == hub.networkKey("203.0.113.6") {
		t.Fatal("same public IPv4 shares a key; another does not")
	}
	if hub.networkKey("2001:db8:1:2::5") != hub.networkKey("2001:db8:1:2::9") || hub.networkKey("2001:db8:1:2::5") == hub.networkKey("2001:db8:1:3::5") {
		t.Fatal("IPv6 compares the /64")
	}
	if key := hub.networkKey("203.0.113.5"); key == "" || key == "203.0.113.5" {
		t.Fatal("the key must not be the address")
	}
}
