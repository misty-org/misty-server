package main

import (
	"strings"
	"testing"
)

func TestExtraIngressRoutesAndPointsDNS(t *testing.T) {
	h := newHarnessWith(t, map[string]string{
		"MISTY_TUNNEL_EXTRA_INGRESS": " Billing.misty.example=http://billing:8091 , status.misty.example=https://status:8443/,",
	})
	if err := h.provision(t); err != nil {
		t.Fatal(err)
	}
	fake := h.fake
	if len(fake.Tunnels) != 1 || len(fake.DNS) != 3 {
		t.Fatalf("resources: tunnels=%d dns=%d", len(fake.Tunnels), len(fake.DNS))
	}
	tunnelID := h.store.State[tunnelState]
	want := []string{
		"cloud.misty.example=" + apiService,
		"billing.misty.example=http://billing:8091",
		"status.misty.example=https://status:8443",
		"=http_status:404",
	}
	if got := fake.Ingress(tunnelID); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("ingress %v, want %v", got, want)
	}
	for _, name := range []string{"cloud.misty.example", "billing.misty.example", "status.misty.example"} {
		record := fake.Record(name)
		if record == nil || record["type"] != "CNAME" || record["proxied"] != true || record["content"] != tunnelID+".cfargotunnel.com" {
			t.Fatalf("dns record for %s: %v", name, record)
		}
	}

	// A record someone set to DNS only is proxied again; nothing is duplicated.
	fake.Record("billing.misty.example")["proxied"] = false
	if err := h.provision(t); err != nil {
		t.Fatal(err)
	}
	if len(fake.DNS) != 3 || fake.Count("PATCH /zones/"+testZone+"/dns_records/") != 1 || fake.Record("billing.misty.example")["proxied"] != true {
		t.Fatal("a second run did not update the extra hostname's record in place")
	}
	if fake.Count("POST /zones/"+testZone+"/dns_records") != 3 {
		t.Fatal("a second run created DNS records again")
	}
}

func TestExtraIngressOutsideTheZoneStopsSetup(t *testing.T) {
	h := newHarnessWith(t, map[string]string{"MISTY_TUNNEL_EXTRA_INGRESS": "billing.other.example=http://billing:8091"})
	err := h.provision(t)
	if err == nil || !strings.Contains(err.Error(), "MISTY_TUNNEL_EXTRA_INGRESS host billing.other.example is not in the zone") {
		t.Fatalf("expected a zone error, got %v", err)
	}
	if fake := h.fake; fake.Count("POST /accounts/") != 0 || fake.Count("PUT /accounts/") != 0 || fake.Count("POST /zones/") != 0 {
		t.Fatal("setup changed Cloudflare despite the zone error")
	}
}

func TestForeignDNSRecordOnAnExtraHostnameStopsSetup(t *testing.T) {
	h := newHarnessWith(t, map[string]string{"MISTY_TUNNEL_EXTRA_INGRESS": "billing.misty.example=http://billing:8091"})
	h.fake.DNS["existing"] = map[string]any{"id": "existing", "type": "A", "name": "billing.misty.example", "content": "203.0.113.1"}
	err := h.provision(t)
	if err == nil || !strings.Contains(err.Error(), "billing.misty.example already has a A record") {
		t.Fatalf("expected a DNS conflict, got %v", err)
	}
	if h.fake.Record("billing.misty.example")["content"] != "203.0.113.1" {
		t.Fatal("setup replaced the operator's record")
	}
}

func TestExtraIngressInputs(t *testing.T) {
	base := map[string]string{
		"MISTY_HOSTNAME": "cloud.misty.example", "CLOUDFLARE_API_TOKEN": "cf-token",
		"CLOUDFLARE_ACCOUNT_ID": testAccount, "CLOUDFLARE_ZONE_ID": testZone,
		"GOOGLE_CLIENT_ID": "g", "GOOGLE_CLIENT_SECRET": "g",
	}
	read := func(value string) (Inputs, error) {
		return readInputs(func(name string) string {
			if name == "MISTY_TUNNEL_EXTRA_INGRESS" {
				return value
			}
			return base[name]
		})
	}
	none, err := read(" , ")
	if err != nil || len(none.ExtraIngress) != 0 {
		t.Fatalf("an empty list: %v %v", none.ExtraIngress, err)
	}
	one, err := read("billing.misty.example=http://billing:8091")
	if err != nil || len(one.ExtraIngress) != 1 || one.ExtraIngress[0] != (IngressRule{Host: "billing.misty.example", Service: "http://billing:8091"}) {
		t.Fatalf("one entry: %v %v", one.ExtraIngress, err)
	}
	if inputsFingerprint(none) == inputsFingerprint(one) {
		t.Fatal("extra hostnames must change the fingerprint")
	}
	for _, value := range []string{
		"billing.misty.example",
		"=http://billing:8091",
		"billing.misty.example=",
		"billing=http://billing:8091",
		"https://billing.misty.example=http://billing:8091",
		"bill_ing.misty.example=http://billing:8091",
		"cloud.misty.example=http://billing:8091",
		"billing.misty.example=http://billing:8091,billing.misty.example=http://other:80",
		"billing.misty.example=billing:8091",
		"billing.misty.example=ftp://billing:21",
		"billing.misty.example=http://billing:8091/v1",
		"billing.misty.example=http://billing:8091?x=1",
		"billing.misty.example=http://user@billing:8091",
		"billing.misty.example=http://",
	} {
		if _, err := read(value); err == nil || !strings.Contains(err.Error(), "MISTY_TUNNEL_EXTRA_INGRESS") {
			t.Errorf("%q: expected an input error, got %v", value, err)
		}
	}
}
