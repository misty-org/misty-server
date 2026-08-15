package api

import (
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	envconfig "github.com/kannachi323/misty/server/internal/platform/config"
)

const (
	connectedDeviceTicketIssuer   = "misty-api"
	connectedDeviceTicketAudience = "misty-device/1"
)

type ConnectedDevicesConfig struct {
	PrivateKey    ed25519.PrivateKey
	KeyID         string
	PairingPepper []byte
	PublicKeys    map[string]ed25519.PublicKey
}

func ConnectedDevicesConfigFromEnv() (ConnectedDevicesConfig, error) {
	privateKey, err := parseEd25519PrivateKey(envconfig.Getenv("MISTY_DEVICE_TICKET_PRIVATE_KEY"))
	if err != nil {
		return ConnectedDevicesConfig{}, fmt.Errorf("MISTY_DEVICE_TICKET_PRIVATE_KEY: %w", err)
	}
	pepper, err := decodeConnectedDeviceSecret("MISTY_DEVICE_PAIRING_PEPPER")
	if err != nil {
		return ConnectedDevicesConfig{}, err
	}
	publicKey := privateKey.Public().(ed25519.PublicKey)
	keyID := connectedDeviceKeyID(publicKey)
	keys := map[string]ed25519.PublicKey{keyID: publicKey}
	for _, encoded := range strings.Split(envconfig.Getenv("MISTY_DEVICE_TICKET_PREVIOUS_PUBLIC_KEYS"), ",") {
		encoded = strings.TrimSpace(encoded)
		if encoded == "" {
			continue
		}
		raw, decodeErr := base64.StdEncoding.DecodeString(encoded)
		if decodeErr != nil || len(raw) != ed25519.PublicKeySize {
			return ConnectedDevicesConfig{}, errors.New("MISTY_DEVICE_TICKET_PREVIOUS_PUBLIC_KEYS must contain comma-separated base64 Ed25519 public keys")
		}
		key := ed25519.PublicKey(raw)
		keys[connectedDeviceKeyID(key)] = key
	}
	return ConnectedDevicesConfig{PrivateKey: privateKey, KeyID: keyID, PairingPepper: pepper, PublicKeys: keys}, nil
}

func TestingConnectedDevicesConfig(privateKey ed25519.PrivateKey, pairingPepper []byte) ConnectedDevicesConfig {
	publicKey := privateKey.Public().(ed25519.PublicKey)
	keyID := connectedDeviceKeyID(publicKey)
	return ConnectedDevicesConfig{PrivateKey: privateKey, KeyID: keyID, PairingPepper: pairingPepper, PublicKeys: map[string]ed25519.PublicKey{keyID: publicKey}}
}

func decodeConnectedDeviceSecret(name string) ([]byte, error) {
	value := strings.TrimSpace(envconfig.Getenv(name))
	raw, err := base64.StdEncoding.DecodeString(value)
	if err != nil || len(raw) < 32 {
		return nil, fmt.Errorf("%s must be at least 32 base64-encoded random bytes", name)
	}
	return raw, nil
}

func connectedDeviceKeyID(publicKey ed25519.PublicKey) string {
	digest := sha256.Sum256(publicKey)
	return base64.RawURLEncoding.EncodeToString(digest[:9])
}

func (c ConnectedDevicesConfig) hashPairingSecret(secret string) string {
	mac := hmac.New(sha256.New, c.PairingPepper)
	mac.Write([]byte(strings.ToUpper(strings.TrimSpace(secret))))
	return fmt.Sprintf("%x", mac.Sum(nil))
}

func (c ConnectedDevicesConfig) valid() bool {
	return len(c.PrivateKey) == ed25519.PrivateKeySize && len(c.PairingPepper) >= 32 && c.KeyID != ""
}

func encodeConnectedDevicePublicKey(key ed25519.PublicKey) string {
	return base64.StdEncoding.EncodeToString(key)
}

func parseConnectedDevicePrivateKeyForTesting(encoded string) (ed25519.PrivateKey, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		return nil, err
	}
	parsed, err := x509.ParsePKCS8PrivateKey(raw)
	if err != nil {
		return nil, err
	}
	key, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return nil, errors.New("not an Ed25519 key")
	}
	return key, nil
}
