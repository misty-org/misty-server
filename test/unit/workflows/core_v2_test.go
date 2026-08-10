package workflow

import (
	"testing"

	. "github.com/kannachi323/misty/server/internal/workflows"
)

func TestCoreRegistryHasOutboundHTTPAndNoGenericWebhookTrigger(t *testing.T) {
	registry := CoreRegistry()
	if _, ok := registry.Resolve("webhook_trigger", 1); ok {
		t.Fatal("generic webhook trigger must not be registered")
	}
	httpNode, ok := registry.Resolve("http_request", 1)
	if !ok {
		t.Fatal("outbound HTTP request node is missing")
	}
	if httpNode.Location != LocationCloud || httpNode.Risk != RiskWrite || httpNode.Idempotent || !httpNode.SupportsReconcile {
		t.Fatalf("unexpected HTTP contract: %+v", httpNode)
	}
}
