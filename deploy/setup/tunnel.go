package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
)

// apiService is where the tunnel sends the hostname's traffic: the API
// container inside the Compose network.
const apiService = "http://api:8080"

type tunnel struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	ConfigSrc string `json:"config_src"`
	DeletedAt string `json:"deleted_at"`
}

type dnsRecord struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	Proxied bool   `json:"proxied"`
}

// provisionTunnel finds or creates the named, remotely managed tunnel, routes
// the hostname to the API (and any extra hostnames to their services) and
// points DNS at it. It records the tunnel ID and the connector token.
func provisionTunnel(ctx context.Context, cf *cloudflare, inputs Inputs, names Names, store *Store) error {
	var zone struct {
		Name    string `json:"name"`
		Account struct {
			ID string `json:"id"`
		} `json:"account"`
	}
	if err := cf.call(ctx, "GET", "zones/"+inputs.ZoneID, nil, &zone); err != nil {
		return err
	}
	if zone.Account.ID != inputs.AccountID {
		return errors.New("CLOUDFLARE_ZONE_ID belongs to another account than CLOUDFLARE_ACCOUNT_ID")
	}
	if !inZone(inputs.Hostname, zone.Name) {
		return fmt.Errorf("MISTY_HOSTNAME %s is not in the zone %s", inputs.Hostname, zone.Name)
	}
	for _, rule := range inputs.ExtraIngress {
		if !inZone(rule.Host, zone.Name) {
			return fmt.Errorf("MISTY_TUNNEL_EXTRA_INGRESS host %s is not in the zone %s", rule.Host, zone.Name)
		}
	}

	query := url.Values{"name": {names.Tunnel}, "is_deleted": {"false"}}
	var tunnels []tunnel
	if err := cf.call(ctx, "GET", cf.accountPath("cfd_tunnel?%s", query.Encode()), nil, &tunnels); err != nil {
		return err
	}
	var matches []tunnel
	for _, item := range tunnels {
		if item.Name == names.Tunnel && item.DeletedAt == "" {
			matches = append(matches, item)
		}
	}
	if len(matches) > 1 {
		return fmt.Errorf("Cloudflare has %d tunnels named %s; delete the extra ones", len(matches), names.Tunnel)
	}
	var current tunnel
	if len(matches) == 1 {
		current = matches[0]
		if current.ConfigSrc == "local" {
			return fmt.Errorf("the tunnel %s is managed locally; delete it or let Misty manage it", names.Tunnel)
		}
	} else {
		body := map[string]any{"name": names.Tunnel, "config_src": "cloudflare"}
		if err := cf.call(ctx, "POST", cf.accountPath("cfd_tunnel"), body, &current); err != nil {
			return err
		}
	}
	if current.ID == "" {
		return errors.New("Cloudflare returned a tunnel without an ID")
	}

	// The API's hostname first, then any extra ones, then the catch-all.
	ingress := []map[string]any{{"hostname": inputs.Hostname, "service": apiService}}
	for _, rule := range inputs.ExtraIngress {
		ingress = append(ingress, map[string]any{"hostname": rule.Host, "service": rule.Service})
	}
	ingress = append(ingress, map[string]any{"service": "http_status:404"})
	config := map[string]any{"config": map[string]any{"ingress": ingress}}
	if err := cf.call(ctx, "PUT", cf.accountPath("cfd_tunnel/%s/configurations", current.ID), config, nil); err != nil {
		return err
	}
	if err := ensureDNS(ctx, cf, inputs.ZoneID, inputs.Hostname, current.ID); err != nil {
		return err
	}
	for _, rule := range inputs.ExtraIngress {
		if err := ensureDNS(ctx, cf, inputs.ZoneID, rule.Host, current.ID); err != nil {
			return err
		}
	}
	var token string
	if err := cf.call(ctx, "GET", cf.accountPath("cfd_tunnel/%s/token", current.ID), nil, &token); err != nil {
		return err
	}
	if token == "" {
		return errors.New("Cloudflare returned an empty tunnel token")
	}
	store.Set("MISTY_CLOUDFLARE_TUNNEL_ID", current.ID)
	store.Set("CLOUDFLARE_TUNNEL_TOKEN", token)
	return nil
}

// ensureDNS keeps one proxied CNAME from a hostname to the tunnel. A record
// that points anywhere else is the operator's; setup stops instead of
// replacing it.
func ensureDNS(ctx context.Context, cf *cloudflare, zoneID, hostname, tunnelID string) error {
	target := tunnelID + ".cfargotunnel.com"
	var records []dnsRecord
	query := url.Values{"name": {hostname}}
	if err := cf.call(ctx, "GET", "zones/"+zoneID+"/dns_records?"+query.Encode(), nil, &records); err != nil {
		return err
	}
	switch {
	case len(records) == 0:
		body := map[string]any{"type": "CNAME", "name": hostname, "content": target, "proxied": true, "comment": "Misty tunnel"}
		return cf.call(ctx, "POST", "zones/"+zoneID+"/dns_records", body, nil)
	case len(records) > 1:
		return fmt.Errorf("%s has %d DNS records; keep none or only the tunnel's CNAME", hostname, len(records))
	}
	record := records[0]
	if record.Type != "CNAME" || record.Content != target {
		return fmt.Errorf("%s already has a %s record for something else; remove it so Misty can route the hostname to its tunnel", hostname, record.Type)
	}
	if !record.Proxied {
		return cf.call(ctx, "PATCH", "zones/"+zoneID+"/dns_records/"+record.ID, map[string]any{"proxied": true}, nil)
	}
	return nil
}

func inZone(host, zone string) bool {
	return host == zone || hasSuffix(host, "."+zone)
}

func hasSuffix(value, suffix string) bool {
	return len(value) > len(suffix) && value[len(value)-len(suffix):] == suffix
}
