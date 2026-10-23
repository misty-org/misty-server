package agent

import (
	"context"
	"testing"
)

func TestAFreeDevelopmentModelIsAvailableOnlyOnADevelopmentStack(t *testing.T) {
	const free = "nim/nvidia/nemotron-3.5-super-vl-120b-a12b"
	t.Setenv("MISTY_ENVIRONMENT", "development")
	t.Setenv("MISTY_ALLOW_MODEL_OVERRIDE", "1")
	if !FrontierModelAvailable(context.Background(), free) {
		t.Fatal("a free development model was refused on a development stack")
	}
	t.Setenv("MISTY_ALLOW_MODEL_OVERRIDE", "")
	if FrontierModelAvailable(context.Background(), free) {
		t.Fatal("a free model was available where requests may not choose their model")
	}
}
