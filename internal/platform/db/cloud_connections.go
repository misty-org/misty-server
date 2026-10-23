package db

import (
	"time"
)

type CloudConnection struct {
	ID, UserID, Provider, Name, AccountID, AccountDisplay string
	ConnectedAccountID, Status, LastErrorCode             string
	CredentialCiphertext, CredentialNonce                 []byte
	KeyVersion                                            int16
	UsesCustomOAuthClient                                 bool
	ExpiresAt                                             *time.Time
	CreatedAt, UpdatedAt                                  time.Time
}
