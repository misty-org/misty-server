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
	case "planner/todoist":
		label, adapter, origins = "Todoist", "todoist", []string{"https://app.todoist.com"}
	default:
		return Provider{}, false
	}
	names = []string{"tasks.create"}
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
