package capabilities

import (
	"encoding/json"
	"github.com/google/uuid"
	"net/url"
)

const PlannerProviderID = "planner/tasks"

func Builtin(name string, version int) (Definition, bool) {
	d, ok := builtinContracts[name][version]
	if !ok {
		return Definition{}, false
	}
	raw, _ := json.Marshal(d)
	var copy Definition
	_ = json.Unmarshal(raw, &copy)
	return copy, true
}
func PlannerProvider() (Provider, error) {
	d, ok := Builtin("tasks.create", 1)
	if !ok {
		return Provider{}, ErrInvalid
	}
	return Provider{ID: PlannerProviderID, Version: 1, Label: "Misty Planner", Route: Route{Kind: "server", Adapter: "planner"}, Capabilities: []Definition{d}}, nil
}
func PlannerTarget(userID, spaceID, spaceName string) Target {
	binding, _ := json.Marshal(map[string]any{"kind": "resource", "resourceId": spaceID})
	return Target{ID: uuid.NewSHA1(uuid.NameSpaceURL, []byte("misty:planner:"+userID+":"+spaceID)).String(), Revision: 1, AppID: "planner", ProviderID: PlannerProviderID, ProviderVersion: 1, SpaceID: spaceID, Label: spaceName + " · Planner", Binding: binding}
}
func PlannerContainer(spaceID string) string {
	return "/spaces/" + url.PathEscape(spaceID) + "/planner/tasks/board"
}
func (t Target) ValidatePlanner(p Provider) error {
	canonical, err := PlannerProvider()
	if err != nil {
		return err
	}
	raw, _ := json.Marshal(p)
	expected, _ := json.Marshal(canonical)
	var b struct {
		Kind       string `json:"kind"`
		ResourceID string `json:"resourceId"`
	}
	if !EqualJSON(raw, expected) || t.AppID != "planner" || t.ProviderID != PlannerProviderID || t.ProviderVersion != 1 || !ValidID(t.ID) || t.Revision < 1 || t.SpaceID == "" || Decode(t.Binding, &b) != nil || b.Kind != "resource" || b.ResourceID != t.SpaceID {
		return ErrInvalid
	}
	return nil
}
