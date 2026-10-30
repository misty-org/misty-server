package agent

import (
	"context"
	"testing"
)

func TestAFreeDevelopmentModelIsAvailableOnADevelopmentStack(t *testing.T) {
	const free = "nim/nvidia/nemotron-3.5-super-vl-120b-a12b"
	t.Setenv("MISTY_ENVIRONMENT", "development")
	t.Setenv("MISTY_ALLOW_MODEL_OVERRIDE", "1")
	if !FrontierModelAvailable(context.Background(), free) || !FreeEvalModel(free) {
		t.Fatal("a free development model was refused on a development stack")
	}
	// Model IDs are no longer checked against a catalog: any provider/model
	// ID is well formed. Only the eval switch makes it a free eval model.
	t.Setenv("MISTY_ALLOW_MODEL_OVERRIDE", "")
	if FreeEvalModel(free) {
		t.Fatal("a free eval model was recognized where requests may not choose their model")
	}
}
