package db

import "testing"

// Recording, revisits, the backlog cap and forgetting run against the full
// schema in test/contract/postgres/memory_spaces_test.go.

func TestSimhashDistance(t *testing.T) {
	if SimhashDistance("00000000000000ff", "00000000000000fc") != 2 || SimhashDistance("zz", "00") != 64 {
		t.Fatal("simhash distance")
	}
}
