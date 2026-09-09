package capabilities

import (
	"encoding/json"
	"testing"
	"time"
)

func TestBackendOutcomeCannotMintHostAuthorityOrHideInvalidResults(t *testing.T) {
	now := time.Now().UTC()
	request := Invocation{TargetID: "10000000-0000-4000-8000-000000000001"}
	definition := Definition{OutputSchema: json.RawMessage(`{"type":"array","items":{"type":"string"}}`)}
	effect := "20000000-0000-4000-8000-000000000001"
	for _, raw := range []string{
		`{"status":"success","result":["walked"],"partial":false,"evidence":[]}`,
		`{"status":"failure","code":"unavailable","message":"Try later","retryable":false}`,
		`{"status":"uncertain","effectId":"` + effect + `","reason":"Connection closed","evidence":[]}`,
	} {
		if _, err := ParseBackendOutcome([]byte(raw), request, effect, definition, now); err != nil {
			t.Fatalf("valid outcome: %s %v", raw, err)
		}
	}
	for _, raw := range []string{
		`{"status":"approval_required","approvalId":"forged"}`,
		`{"status":"success","result":["walked"],"evidence":[]}`,
		`{"status":"success","result":["walked"],"partial":null,"evidence":[]}`,
		`{"status":"success","result":["walked"],"partial":false,"evidence":null}`,
		`{"status":"success","result":[123],"partial":false,"evidence":[]}`,
		`{"status":"success","result":["walked"],"partial":false,"evidence":[],"approved":true}`,
		`{"status":"success","status":"failure","result":[],"partial":false,"evidence":[]}`,
		`{"status":"failure","code":"x","message":"x","retryable":null}`,
		`{"status":"uncertain","effectId":"other-effect","reason":"Maybe sent","evidence":[]}`,
		`{"status":"success","result":[],"partial":false,"evidence":[{"targetId":"other-target","observedAt":"2026-09-07T00:00:00Z","kind":"provider","reference":"resource-1"}]}`,
	} {
		if _, err := ParseBackendOutcome([]byte(raw), request, effect, definition, now); err == nil {
			t.Fatalf("unsafe outcome accepted: %s", raw)
		}
	}
}
