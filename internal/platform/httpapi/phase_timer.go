package api

import (
	"fmt"
	"log"
	"strings"
	"time"
)

// phaseTimer logs how long each phase of a slow handler took, as one line, for finding where
// an agent's tool calls spend their time (docs/plans/agent-harness-v2.md, change 5).
type phaseTimer struct {
	name, run   string
	start, last time.Time
	phases      []string
}

func newPhaseTimer(name, run string) *phaseTimer {
	now := time.Now()
	return &phaseTimer{name: name, run: run, start: now, last: now}
}

func (t *phaseTimer) mark(phase string) {
	now := time.Now()
	t.phases = append(t.phases, fmt.Sprintf("%s=%dms", phase, now.Sub(t.last).Milliseconds()))
	t.last = now
}

func (t *phaseTimer) log() {
	log.Printf("%s run=%s total=%dms %s", t.name, t.run, time.Since(t.start).Milliseconds(), strings.Join(t.phases, " "))
}
