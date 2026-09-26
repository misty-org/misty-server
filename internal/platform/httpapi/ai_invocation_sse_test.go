package api

import (
	"errors"
	"net/http/httptest"
	"testing"
	"time"
)

type invocationSSEWriter struct {
	*httptest.ResponseRecorder
	deadline  time.Time
	writes    int
	failWrite bool
	failFlush bool
}

func (w *invocationSSEWriter) SetWriteDeadline(deadline time.Time) error {
	w.deadline = deadline
	return nil
}
func (w *invocationSSEWriter) Write(p []byte) (int, error) {
	if !w.deadline.After(time.Now()) {
		return 0, errors.New("expired write deadline")
	}
	if w.failWrite {
		return 0, errors.New("disconnected")
	}
	w.writes++
	return w.ResponseRecorder.Write(p)
}
func (w *invocationSSEWriter) FlushError() error {
	if w.failFlush {
		return errors.New("flush disconnected")
	}
	w.ResponseRecorder.Flush()
	return nil
}
func TestInvocationSSERenewsDeadlineForEventsAndHeartbeats(t *testing.T) {
	w := &invocationSSEWriter{ResponseRecorder: httptest.NewRecorder()}
	for _, frame := range []string{"", "id: 1\ndata: {}\n\n", ": keep-alive\n\n", "id: 2\ndata: {}\n\n"} {
		w.deadline = time.Now().Add(-time.Minute)
		before := time.Now()
		if err := writeAIInvocationSSE(w, frame); err != nil {
			t.Fatal(err)
		}
		if w.deadline.Before(before.Add(29*time.Second)) || w.deadline.After(time.Now().Add(31*time.Second)) {
			t.Fatalf("unbounded write: %v", w.deadline)
		}
	}
	if w.writes != 4 || !w.Flushed {
		t.Fatal("frames were not flushed")
	}
}
func TestInvocationSSEStopsOnWriteOrFlushFailure(t *testing.T) {
	for _, flush := range []bool{false, true} {
		w := &invocationSSEWriter{ResponseRecorder: httptest.NewRecorder(), failWrite: !flush, failFlush: flush}
		if err := writeAIInvocationSSE(w, "data: {}\n\n"); err == nil {
			t.Fatal("ignored disconnect")
		}
	}
}
