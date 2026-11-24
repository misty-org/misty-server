package collab

import (
	"github.com/misty-org/misty-server/internal/collab/wire"
	"github.com/misty-org/misty-server/internal/collab/ydoc"
)

const remeasureAfterBytes = 256 * 1024

type gateResult struct {
	status         string
	projectedBytes int
}

type sizeGate struct {
	doc           *ydoc.Doc
	measuredBytes int
	appliedSince  int
}

func (g *sizeGate) measured(bytes int) {
	g.measuredBytes = bytes
	g.appliedSince = 0
}

func (g *sizeGate) measure() int {
	bytes, err := g.doc.Size()
	if err != nil {
		return g.measuredBytes + g.appliedSince
	}
	g.measured(bytes)
	return bytes
}

func (g *sizeGate) check(update []byte) *gateResult {
	estimate := g.measuredBytes + g.appliedSince + len(update)
	if estimate < DocumentWarningBytes {
		return nil
	}
	if estimate > MaxDocumentBytes || g.appliedSince >= remeasureAfterBytes {
		estimate = g.measure() + len(update)
	}
	if estimate > MaxDocumentBytes {
		projected, ok := projectedBytes(g.doc, update)
		if !ok {
			return nil
		}
		if projected > MaxDocumentBytes {
			return &gateResult{status: "blocked", projectedBytes: projected}
		}
		estimate = projected
	}
	if estimate >= DocumentWarningBytes {
		return &gateResult{status: "warning", projectedBytes: estimate}
	}
	return nil
}

func projectedBytes(doc *ydoc.Doc, update []byte) (int, bool) {
	projected, err := doc.Copy()
	if err != nil {
		return 0, false
	}
	defer projected.Destroy()
	if err := projected.Apply(update, nil); err != nil {
		return 0, false
	}
	size, err := projected.Size()
	return size, err == nil
}

type documentStatus struct {
	Type          string `json:"type"`
	Status        string `json:"status"`
	Code          string `json:"code,omitempty"`
	DocumentBytes int    `json:"document_bytes"`
	MaximumBytes  int    `json:"maximum_bytes"`
}

func statusFrame(status string, bytes int, code string) wire.Frame {
	return wire.TextFrame(documentStatus{Type: "document_status", Status: status, Code: code,
		DocumentBytes: bytes, MaximumBytes: MaxDocumentBytes})
}
