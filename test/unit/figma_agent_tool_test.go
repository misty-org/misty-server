package unit

import (
	"strings"
	"testing"

	api "github.com/kannachi323/misty/server/internal/platform/httpapi"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestFigmaAgentCommentExecutesWithoutApproval(t *testing.T) {
	descriptors := api.TestingCanonicalAgentToolboxDescriptors("figma")
	found := false
	for _, descriptor := range descriptors {
		if descriptor.Name != "provider.figma.write" {
			continue
		}
		found = true
		if descriptor.Approval != "none" || descriptor.Risk != "write" || descriptor.AuditEvent == "" || descriptor.RequiredPermission != db.PermissionIntegrationsManage {
			t.Fatalf("Figma write descriptor requires approval or lacks ownership/audit policy: %#v", descriptor)
		}
		if !strings.Contains(string(descriptor.InputSchema), "binding_id") || !strings.Contains(string(descriptor.InputSchema), "message") {
			t.Fatalf("Figma write schema=%s", descriptor.InputSchema)
		}
	}
	if !found {
		t.Fatal("provider.figma.write descriptor missing")
	}
}
