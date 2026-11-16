package fakecloudflare

import "strings"

func (f *Fake) inZone(name string) bool {
	return name == f.ZoneName || strings.HasSuffix(name, "."+f.ZoneName)
}

// validIngress accepts what Cloudflare accepts for a tunnel's ingress: rules
// with distinct hostnames and a service each, ending in one catch-all rule
// without a hostname.
func validIngress(config any) bool {
	body, _ := config.(map[string]any)
	rules, _ := body["ingress"].([]any)
	if len(rules) == 0 {
		return false
	}
	seen := map[string]bool{}
	for index, item := range rules {
		rule, _ := item.(map[string]any)
		hostname, _ := rule["hostname"].(string)
		service, _ := rule["service"].(string)
		last := index == len(rules)-1
		if service == "" || (last != (hostname == "")) || seen[hostname] {
			return false
		}
		seen[hostname] = true
	}
	return true
}

// Ingress returns a tunnel's ingress rules in order as hostname=service, with
// the catch-all as =service.
func (f *Fake) Ingress(tunnelID string) []string {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	body, _ := f.Configs[tunnelID].(map[string]any)
	rules, _ := body["ingress"].([]any)
	var out []string
	for _, item := range rules {
		rule, _ := item.(map[string]any)
		hostname, _ := rule["hostname"].(string)
		service, _ := rule["service"].(string)
		out = append(out, hostname+"="+service)
	}
	return out
}

// Record returns the DNS record for a name, or nil.
func (f *Fake) Record(name string) map[string]any {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	for _, record := range f.DNS {
		if record["name"] == name {
			return record
		}
	}
	return nil
}
