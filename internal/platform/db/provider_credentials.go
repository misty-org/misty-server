package db

import (
	"time"
)

type ProviderCredential struct {
	ID             string
	IntegrationID  string
	SpaceID        string
	UserID         string
	Provider       string
	Ciphertext     []byte
	Nonce          []byte
	KeyVersion     int16
	AccountID      string
	AccountDisplay string
	ExpiresAt      *time.Time
}
