package unit

import (
	"encoding/json"
	"strings"
	"testing"

	serveragent "github.com/kannachi323/misty/server/internal/agents"
	api "github.com/kannachi323/misty/server/internal/platform/httpapi"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

// Code projects (docs/plans/agent-code-projects.md): a write-shared folder
// grants the code tools and nothing else, and no other grant grants them.
func TestProjectFolderGrantsOnlyCodeTools(t *testing.T) {
	valid := []byte(`[{"device_id":"device-1","kind":"project_folder","opaque_ref":"scope_a","capabilities":["code.read","code.edit","code.run","code.preview","files.read","files.send"]}]`)
	if err := api.TestingValidateAIInvocationDeviceContexts([]byte(`[]`), valid, ""); err != nil {
		t.Fatalf("a project grant was rejected: %v", err)
	}
	for name, contexts := range map[string]string{
		"project deleting files":   `[{"device_id":"device-1","kind":"project_folder","opaque_ref":"scope_a","capabilities":["files.delete"]}]`,
		"project with browser":     `[{"device_id":"device-1","kind":"project_folder","opaque_ref":"scope_a","capabilities":["code.run","browser.navigate"]}]`,
		"read-only folder running": `[{"device_id":"device-1","kind":"local_folder","opaque_ref":"scope_a","capabilities":["code.run"]}]`,
		"workspace editing":        `[{"device_id":"device-1","kind":"workspace","opaque_ref":"workspace:device-1","capabilities":["code.write"]}]`,
		"empty":                    `[{"device_id":"device-1","kind":"project_folder","opaque_ref":"scope_a","capabilities":[]}]`,
	} {
		if err := api.TestingValidateAIInvocationDeviceContexts([]byte(`[]`), []byte(contexts), ""); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestCodeToolsMatchTheProjectGrant(t *testing.T) {
	descriptors := api.TestingProjectToolDescriptors("site")
	if len(descriptors) != 20 {
		t.Fatalf("got %d code and git tools, want 20", len(descriptors))
	}
	for _, descriptor := range descriptors {
		capability, _ := json.Marshal([]string{descriptor.Name})
		if !(strings.HasPrefix(descriptor.Name, "code.") || strings.HasPrefix(descriptor.Name, "git.")) || !db.DeviceContextCapabilitiesAllowed("project_folder", capability) {
			t.Errorf("%s is not grantable by a project folder", descriptor.Name)
		}
		if strings.Contains(string(descriptor.InputSchema), `"project"`) {
			t.Errorf("%s asks which project when there is only one", descriptor.Name)
		}
		readOnly := map[string]bool{"code.read": true, "code.list": true, "code.find": true, "code.search": true, "code.logs": true, "code.processes": true, "git.status": true, "git.diff": true, "git.log": true}
		if readOnly[descriptor.Name] != (descriptor.Risk == serveragent.RiskRead) {
			t.Errorf("%s has risk %s", descriptor.Name, descriptor.Risk)
		}
		if descriptor.Risk != serveragent.RiskRead && descriptor.AuditEvent == "" {
			t.Errorf("%s changes things but has no audit event", descriptor.Name)
		}
	}
	for _, descriptor := range api.TestingProjectToolDescriptors("site", "blog") {
		var schema struct {
			Required []string `json:"required"`
		}
		_ = json.Unmarshal(descriptor.InputSchema, &schema)
		if !strings.Contains(strings.Join(schema.Required, ","), "project") {
			t.Errorf("%s must name the project when there are two", descriptor.Name)
		}
	}
}
