package db

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

var (
	ErrAgentNotFound       = errors.New("Space Agent not found")
	ErrDeviceNotFound      = errors.New("trusted device not found")
	ErrDeviceRequestReplay = errors.New("trusted device request was already used")
	ErrAgentJobNotFound    = errors.New("workflow device node job not found")
	ErrInvalidJobState     = errors.New("invalid workflow device node state")
	ErrInvalidLease        = errors.New("invalid or expired workflow device node lease")
)

type TrustedDevice struct {
	ID           string          `json:"id"`
	UserID       string          `json:"userId"`
	Name         string          `json:"name"`
	PublicKey    string          `json:"publicKey"`
	KeyAlgorithm string          `json:"keyAlgorithm"`
	Capabilities json.RawMessage `json:"capabilities"`
	LastSeenAt   time.Time       `json:"lastSeenAt"`
	CreatedAt    time.Time       `json:"createdAt"`
	UpdatedAt    time.Time       `json:"updatedAt"`
	RevokedAt    *time.Time      `json:"revokedAt,omitempty"`
}

func (db *Database) agentTx(userID string, fn func(*sql.Tx) error) error {
	return db.TestingWithRLSContext(context.Background(), userRLSSettings(userID), fn)
}

func (db *Database) RegisterTrustedDevice(userID, name, publicKey string, capabilities json.RawMessage) (*TrustedDevice, error) {
	if len(capabilities) == 0 {
		capabilities = json.RawMessage(`{}`)
	}
	device := &TrustedDevice{}
	err := db.agentTx(userID, func(tx *sql.Tx) error {
		return scanDevice(tx.QueryRow(`INSERT INTO trusted_devices(id,user_id,name,public_key,capabilities) VALUES($1,$2,$3,$4,$5)
			ON CONFLICT(user_id,public_key) DO UPDATE SET name=EXCLUDED.name,capabilities=EXCLUDED.capabilities,last_seen_at=NOW(),updated_at=NOW(),revoked_at=NULL
			RETURNING id,user_id,name,public_key,key_algorithm,capabilities,last_seen_at,revoked_at,created_at,updated_at`,
			"device_"+uuid.NewString(), userID, name, publicKey, capabilities), device)
	})
	return device, err
}

func (db *Database) TrustedDevices(userID string) ([]TrustedDevice, error) {
	devices := []TrustedDevice{}
	err := db.agentTx(userID, func(tx *sql.Tx) error {
		rows, err := tx.Query(`SELECT id,user_id,name,public_key,key_algorithm,capabilities,last_seen_at,revoked_at,created_at,updated_at FROM trusted_devices WHERE user_id=$1 ORDER BY created_at`, userID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var device TrustedDevice
			if err := scanDevice(rows, &device); err != nil {
				return err
			}
			devices = append(devices, device)
		}
		return rows.Err()
	})
	return devices, err
}

func (db *Database) HeartbeatTrustedDevice(userID, deviceID string, capabilities json.RawMessage) (*TrustedDevice, error) {
	if len(capabilities) == 0 {
		capabilities = json.RawMessage(`{}`)
	}
	device := &TrustedDevice{}
	err := db.agentTx(userID, func(tx *sql.Tx) error {
		err := scanDevice(tx.QueryRow(`UPDATE trusted_devices SET capabilities=$1,last_seen_at=NOW(),updated_at=NOW() WHERE id=$2 AND user_id=$3 AND revoked_at IS NULL RETURNING id,user_id,name,public_key,key_algorithm,capabilities,last_seen_at,revoked_at,created_at,updated_at`, capabilities, deviceID, userID), device)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrDeviceNotFound
		}
		return err
	})
	return device, err
}

func (db *Database) RevokeTrustedDevice(userID, deviceID string) error {
	return db.agentTx(userID, func(tx *sql.Tx) error {
		result, err := tx.Exec(`UPDATE trusted_devices SET revoked_at=COALESCE(revoked_at,NOW()),updated_at=NOW() WHERE id=$1 AND user_id=$2`, deviceID, userID)
		if err != nil {
			return err
		}
		count, _ := result.RowsAffected()
		if count == 0 {
			return ErrDeviceNotFound
		}
		return nil
	})
}

func (db *Database) TrustedDevicePublicKey(userID, deviceID string) (string, error) {
	var publicKey string
	err := db.agentTx(userID, func(tx *sql.Tx) error {
		err := tx.QueryRow(`SELECT public_key FROM trusted_devices WHERE id=$1 AND user_id=$2 AND revoked_at IS NULL`, deviceID, userID).Scan(&publicKey)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrDeviceNotFound
		}
		return err
	})
	return publicKey, err
}

func (db *Database) ConsumeTrustedDeviceNonce(userID, deviceID, nonce string, expiresAt time.Time) (string, error) {
	var publicKey string
	err := db.agentTx(userID, func(tx *sql.Tx) error {
		if _, err := tx.Exec(`DELETE FROM trusted_device_request_nonces WHERE owner_user_id=$1 AND expires_at <= NOW()`, userID); err != nil {
			return err
		}
		if err := tx.QueryRow(`SELECT public_key FROM trusted_devices WHERE id=$1 AND user_id=$2 AND revoked_at IS NULL`, deviceID, userID).Scan(&publicKey); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrDeviceNotFound
			}
			return err
		}
		result, err := tx.Exec(`INSERT INTO trusted_device_request_nonces(device_id,owner_user_id,nonce,expires_at) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, deviceID, userID, nonce, expiresAt)
		if err != nil {
			return err
		}
		inserted, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if inserted != 1 {
			return ErrDeviceRequestReplay
		}
		return nil
	})
	return publicKey, err
}

type scanner interface{ Scan(...any) error }

func scanDevice(row scanner, device *TrustedDevice) error {
	return row.Scan(&device.ID, &device.UserID, &device.Name, &device.PublicKey, &device.KeyAlgorithm, &device.Capabilities, &device.LastSeenAt, &device.RevokedAt, &device.CreatedAt, &device.UpdatedAt)
}

func TestingSecureToken() (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func TestingHashToken(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
