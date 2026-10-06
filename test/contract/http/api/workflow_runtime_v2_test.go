package api

import (
	"encoding/json"
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/httpapi"

)



func TestDecodeJSONObjectAcceptsFencedStructuredAgentOutput(t *testing.T) {
	if got := string(TestingDecodeJSONObject("```json\n{\"answer\":\"done\"}\n```")); got != `{"answer":"done"}` {
		t.Fatalf("decodeJSONObject() = %q", got)
	}
}

func TestWorkflowResourceIdentityUsesStableProviderResourceAndFingerprint(t *testing.T) {
	key, fingerprint := TestingWorkflowResourceIdentity(json.RawMessage(`{"destination":".summaries"}`), json.RawMessage(`{"content":{"providerId":"library","resourceId":"item-1","fingerprint":"sha-1"}}`))
	if key != "library:item-1" || fingerprint != "sha-1" {
		t.Fatalf("identity = %q, %q", key, fingerprint)
	}
}

func TestWorkflowEventIdentityAcceptsCanonicalAndProviderEventFields(t *testing.T) {
	provider, eventID := TestingWorkflowEventIdentity(json.RawMessage(`{"provider":"device","eventId":"evt-1"}`))
	if provider != "device" || eventID != "evt-1" {
		t.Fatalf("identity = %q, %q", provider, eventID)
	}
}



