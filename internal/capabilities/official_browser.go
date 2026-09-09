package capabilities

// Official browser providers are shipped host adapters, like Planner's server
// adapter. Downloaded providers continue to use signed manifests. These
// declarations never certify a live account or authorize an external effect.
func OfficialBrowserProvider(id string, version int) (Provider, bool) {
	if version != 1 {
		return Provider{}, false
	}
	var label, adapter string
	var origins, names []string
	switch id {
	case "inbox/gmail":
		label, adapter, origins = "Gmail", "gmail", []string{"https://mail.google.com"}
	case "inbox/outlook":
		label, adapter, origins = "Outlook", "outlook", []string{"https://outlook.live.com", "https://outlook.office.com", "https://outlook.office365.com", "https://outlook.cloud.microsoft"}
	case "planner/todoist":
		label, adapter, origins = "Todoist", "todoist", []string{"https://app.todoist.com"}
	default:
		return Provider{}, false
	}
	if adapter == "todoist" {
		names = []string{"tasks.create"}
	} else {
		names = []string{"inbox.read", "inbox.draft", "inbox.send"}
	}
	p := Provider{ID: id, Version: version, Label: label, Route: Route{Kind: "browser", Adapter: adapter, AdapterVersion: 1, Origins: origins, Hints: []string{}}, Capabilities: []Definition{}}
	for _, name := range names {
		d, ok := Builtin(name, 1)
		if !ok {
			return Provider{}, false
		}
		p.Capabilities = append(p.Capabilities, d)
	}
	return p, true
}

func OfficialBrowserScopes(providerID, capability string) ([]string, bool) {
	p, ok := OfficialBrowserProvider(providerID, 1)
	if !ok {
		return nil, false
	}
	for _, d := range p.Capabilities {
		if d.Name != capability {
			continue
		}
		if capability == "inbox.read" {
			return []string{"mail.read", "browser.inspect"}, true
		}
		if capability == "tasks.create" {
			return []string{"tasks.write", "browser.inspect", "browser.interact"}, true
		}
		return []string{"mail.write", "browser.inspect", "browser.interact"}, true
	}
	return nil, false
}
