package fakecloudflare

import "strings"

func (f *Fake) inZone(name string) bool {
	return name == f.ZoneName || strings.HasSuffix(name, "."+f.ZoneName)
}

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
		path, _ := rule["path"].(string)
		last := index == len(rules)-1
		if service == "" || (last != (hostname == "")) || seen[hostname+"\x00"+path] {
			return false
		}
		seen[hostname+"\x00"+path] = true
	}
	return true
}

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
