package capabilities

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestRoutineValuesUseConfirmedResultsAndTypedPaths(t *testing.T) {
	trigger := json.RawMessage(`{"ready":true}`)
	values := map[string]json.RawMessage{"read": json.RawMessage(`{"items":[{"title":"Morning"}],"0":"object key"}`)}
	input := json.RawMessage(`{"kind":"object","fields":{"title":{"kind":"reference","source":{"kind":"step","stepId":"read"},"path":["items",0,"title"]},"ready":{"kind":"reference","source":{"kind":"trigger"},"path":["ready"]}}}`)
	result, err := RoutineResolveValue(input, trigger, values)
	if err != nil || !EqualJSON(result, []byte(`{"title":"Morning","ready":true}`)) {
		t.Fatalf("resolved: %s %v", result, err)
	}
	for _, path := range []string{`["items","0"]`, `[0]`, `["missing"]`} {
		raw := json.RawMessage(`{"kind":"reference","source":{"kind":"step","stepId":"read"},"path":` + path + `}`)
		if _, err := RoutineResolveValue(raw, trigger, values); !errors.Is(err, ErrRoutineReferenceMissing) {
			t.Fatalf("invalid path %s: %v", path, err)
		}
	}
	condition := json.RawMessage(`{"kind":"all","conditions":[{"kind":"exists","reference":{"kind":"reference","source":{"kind":"step","stepId":"read"},"path":["items",0]}},{"kind":"equals","left":{"kind":"reference","source":{"kind":"trigger"},"path":["ready"]},"right":{"kind":"literal","value":true}}]}`)
	if ok, err := RoutineEvaluateCondition(condition, trigger, values); err != nil || !ok {
		t.Fatalf("condition: %v %v", ok, err)
	}
	delete(values, "read")
	if ok, err := RoutineEvaluateCondition(condition, trigger, values); err != nil || ok {
		t.Fatalf("absent receipt: %v %v", ok, err)
	}
}
