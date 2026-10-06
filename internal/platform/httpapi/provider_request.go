package api

import (
	"fmt"
)

type providerAPIError struct {
	Status     int
	BodyDigest string
}

func (e *providerAPIError) Error() string {
	return fmt.Sprintf("provider request returned %d (body %s)", e.Status, e.BodyDigest)
}

