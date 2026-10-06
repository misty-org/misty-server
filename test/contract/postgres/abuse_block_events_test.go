package db

import (
	"testing"
	"time"
)


import ()

func abuseHint(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Fatal("missing abuse hint")
	}
}

func abuseQuiet(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
		t.Fatal("unexpected abuse hint")
	case <-time.After(30 * time.Millisecond):
	}
}
