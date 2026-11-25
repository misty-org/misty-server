package ydoc

import "testing"

func TestADeeplyNestedValueStopsAtTheNestingLimit(t *testing.T) {
	var deep any = "bottom"
	for i := 0; i < 1_000_000; i++ {
		deep = []any{deep}
	}
	if _, err := marshalShared(deep); err != nil {
		t.Fatal(err)
	}
	plainValue(deep)
	_ = jsString(deep)

	levels := 0
	for value := jsonValue(deep); value != nil; levels++ {
		list, ok := value.([]any)
		if !ok {
			t.Fatalf("level %d is %T", levels, value)
		}
		value = list[0]
	}
	if levels != maxNesting+1 {
		t.Fatalf("kept %d levels, want %d", levels, maxNesting+1)
	}
}
