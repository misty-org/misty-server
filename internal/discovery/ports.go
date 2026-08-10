// Package discovery owns metadata extraction, people clustering, semantic
// indexing, Smart Library, and media search.
package discovery

import "context"

type Document struct {
	ID       string
	Text     string
	Metadata map[string]string
}

type Match struct {
	ID    string
	Score float64
}

// Analyzer converts a Library document into a semantic representation.
type Analyzer interface {
	Embed(context.Context, Document) ([]float32, error)
}

// Index is the search capability consumed by discovery use cases.
type Index interface {
	Upsert(context.Context, Document, []float32) error
	Search(context.Context, []float32, int) ([]Match, error)
}
