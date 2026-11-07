package api

import (
	"regexp"
	"sync"
	"time"

	"github.com/misty-org/misty-server/internal/platform/db"
)

var (
	deviceIDPattern    = regexp.MustCompile(`^device_[0-9a-f-]{36}$`)
	deviceNoncePattern = regexp.MustCompile(`^[A-Za-z0-9+/=_-]{16,200}$`)
)

const (
	deviceSignatureMaxSkew = 5 * time.Minute
	deviceSignedBodyLimit  = 2 << 20
)

// DevicesService exposes trusted-device identity, for sync and Connected Devices.
type DevicesService struct {
	database         *db.Database
	connectedDevices ConnectedDevicesConfig
	deviceHub        *deviceHub
	deviceHubOnce    sync.Once
}

func NewDevicesService(database *db.Database) *DevicesService {
	return &DevicesService{database: database}
}
