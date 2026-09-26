package db

import "errors"

var ErrSDKVersionConflict = errors.New("immutable provider version conflicts with account configuration")
var ErrSDKProviderUnavailable = errors.New("provider is unavailable")
