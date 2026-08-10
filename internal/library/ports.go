// Package library owns object storage, uploads, quotas, organization,
// sharing, edits, previews, renditions, and reconciliation.
package library

import (
	"context"
	"io"
)

type ObjectKey string

// ObjectStore is the storage capability consumed by Library use cases.
type ObjectStore interface {
	Put(context.Context, ObjectKey, io.Reader, int64, string) error
	Get(context.Context, ObjectKey) (io.ReadCloser, error)
	Delete(context.Context, ObjectKey) error
}

// Catalog persists library metadata independently of the object provider.
type Catalog interface {
	ReserveBytes(context.Context, string, int64) error
	ReleaseBytes(context.Context, string, int64) error
}
