package unit

import (
	"strings"
	"testing"

	api "github.com/kannachi323/misty/server/internal/platform/httpapi"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestFigmaAgentCommentRequiresInteractiveApproval(t *testing.T) {
	descriptors := api.TestingCanonicalAgentToolboxDescriptors("figma")
	found := false
	for _, descriptor := range descriptors {
		if descriptor.Name != "provider.figma.write" {
			continue
		}
		found = true
		if descriptor.Approval != "interactive" || descriptor.Risk != "write" || descriptor.AuditEvent == "" || descriptor.RequiredPermission != db.PermissionIntegrationsManage {
			t.Fatalf("Figma write descriptor is not approval gated: %#v", descriptor)
		}
		if !strings.Contains(string(descriptor.InputSchema), "binding_id") || !strings.Contains(string(descriptor.InputSchema), "message") {
			t.Fatalf("Figma write schema=%s", descriptor.InputSchema)
		}
	}
	if !found {
		t.Fatal("provider.figma.write descriptor missing")
	}
}
