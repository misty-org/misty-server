package unit

import (
	"strings"
	"testing"

	api "github.com/kannachi323/misty/server/internal/platform/httpapi"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestGitHubAgentWriteExecutesWithoutApproval(t *testing.T) {
	descriptors := api.TestingCanonicalAgentToolboxDescriptors("github")
	found := false
	for _, descriptor := range descriptors {
		if descriptor.Name != "provider.github.write" {
			continue
		}
		found = true
		if descriptor.Approval != "none" || descriptor.Risk != "write" || descriptor.AuditEvent == "" || descriptor.RequiredPermission != db.PermissionIntegrationsManage {
			t.Fatalf("GitHub write descriptor requires approval or lacks ownership/audit policy: %#v", descriptor)
		}
		if !strings.Contains(string(descriptor.InputSchema), "create_pull_request") || !strings.Contains(string(descriptor.InputSchema), "workspace_id") {
			t.Fatalf("GitHub write schema=%s", descriptor.InputSchema)
		}
	}
	if !found {
		t.Fatal("provider.github.write descriptor missing")
	}
}
