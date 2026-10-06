package db

import (
	"testing"
	"time"
)

func resourceHint(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Fatal("missing resource hint")
	}
}
func resourceQuiet(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
		t.Fatal("unexpected resource hint")
	case <-time.After(30 * time.Millisecond):
	}
}
