package workflow

import (
	"errors"
)

var (
	ErrProviderMissing   = errors.New("workflow provider missing")
	ErrCapabilityDenied  = errors.New("workflow capability denied")
)







