package db

import (
	"fmt"
	"testing"
)

func TestWorkerHubTargetsOneResourceAndResetsAll(t *testing.T) {
	target, other := make(chan struct{}, 1), make(chan struct{}, 1)
	h := &workerEventHub{subscribers: map[string]map[chan struct{}]struct{}{
		"resource-lease:a": {target: {}}, "resource-lease:b": {other: {}},
	}}
	for i := 0; i < 100; i++ {
		h.publish("resource-lease:a")
	}
	if len(target) != 1 || len(other) != 0 {
		t.Fatal("hints leaked or did not coalesce")
	}
	<-target
	h.publish("")
	if len(target) != 1 || len(other) != 1 {
		t.Fatal("reset failed to cover subscriptions")
	}
}
func TestWorkerResourceTopicValidation(t *testing.T) {
	for _, topic := range []string{"resource-lease:", "resource-lease:private-path", "resource-lease:" + fmt.Sprintf("%064s", "z"), "unknown"} {
		if resourceLeaseTopic(topic) {
			t.Fatalf("accepted malformed topic %q", topic)
		}
	}
	if !resourceLeaseTopic("resource-lease:" + fmt.Sprintf("%064x", 1)) {
		t.Fatal("rejected digest topic")
	}
}
func BenchmarkWorkerHintIndexedByResource(b *testing.B) {
	for _, count := range []int{1, 10000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			h := &workerEventHub{subscribers: map[string]map[chan struct{}]struct{}{}}
			for i := 0; i < count; i++ {
				h.subscribers[fmt.Sprint(i)] = map[chan struct{}]struct{}{make(chan struct{}, 1): {}}
			}
			var ch chan struct{}
			for candidate := range h.subscribers["0"] {
				ch = candidate
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				h.publish("0")
				<-ch
			}
		})
	}
}
