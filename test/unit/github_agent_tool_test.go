package unit

import (
	"strings"
	"testing"

	api "github.com/kannachi323/misty/server/internal/platform/httpapi"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestGitHubAgentWriteRequiresInteractiveApproval(t *testing.T) {
	descriptors := api.TestingCanonicalAgentToolboxDescriptors("github")
	found := false
	for _, descriptor := range descriptors {
		if descriptor.Name != "provider.github.write" {
			continue
		}
		found = true
		if descriptor.Approval != "interactive" || descriptor.Risk != "write" || descriptor.AuditEvent == "" || descriptor.RequiredPermission != db.PermissionIntegrationsManage {
			t.Fatalf("GitHub write descriptor is not approval gated: %#v", descriptor)
		}
		if !strings.Contains(string(descriptor.InputSchema), "create_pull_request") || !strings.Contains(string(descriptor.InputSchema), "workspace_id") {
			t.Fatalf("GitHub write schema=%s", descriptor.InputSchema)
		}
	}
	if !found {
		t.Fatal("provider.github.write descriptor missing")
	}
}
