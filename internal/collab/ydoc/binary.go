package ydoc

import "github.com/reearth/ygo/encoding"

type binarySpan struct{ start, content, end int }

func binaryAsAny(update []byte) []byte {
	spans, ok := binarySpans(update)
	if !ok || len(spans) == 0 {
		return update
	}
	out := make([]byte, 0, len(update)+2*len(spans))
	prev := 0
	for _, span := range spans {
		out = append(out, update[prev:span.start]...)
		out = append(out, update[span.start]&^0x1f|contentAny)
		out = append(out, update[span.start+1:span.content]...)
		out = append(out, 1, 116)
		out = append(out, update[span.content:span.end]...)
		prev = span.end
	}
	return append(out, update[prev:]...)
}

func binarySpans(update []byte) ([]binarySpan, bool) {
	dec := encoding.NewDecoder(update)
	at := func() int { return len(update) - dec.Remaining() }
	numClients, err := dec.ReadVarUint()
	if err != nil {
		return nil, false
	}
	var spans []binarySpan
	total := 0
	for c := uint64(0); c < numClients; c++ {
		numStructs, err := dec.ReadVarUint()
		if err != nil {
			return nil, false
		}
		client, err := dec.ReadVarUint()
		if err != nil {
			return nil, false
		}
		clock, err := dec.ReadVarUint()
		if err != nil {
			return nil, false
		}
		for i := uint64(0); i < numStructs; i++ {
			total++
			if total > maxScanStructs {
				return nil, false
			}
			start := at()
			s, err := scanOne(dec, client, clock)
			if err != nil {
				return nil, false
			}
			if s.item && s.content == contentBinary {
				spans = append(spans, binarySpan{start: start, content: len(update) - s.contentLeft, end: at()})
			}
			clock += s.length
		}
	}
	return spans, true
}
