// Package serverenv holds the values a server stack works out instead of
// storing: every generated key, derived from one root secret, and the
// settings that follow from the hostname. The setup container and the API
// both use it, so they always agree on a value without either writing it down.
package serverenv

import (
	"crypto/ed25519"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

const (
	// RootName is the stack's one stored secret, 32 random bytes in standard
	// base64. Only the setup container reads it.
	RootName = "MISTY_SERVER_SECRET"
	// APISecretName is the API's share of the root: the API and the console
	// derive their keys from it, and never see the root, the database
	// superuser password or the backup key.
	APISecretName = "MISTY_API_SECRET"
	// VersionsName lists the keys rotated away from version 1, such as
	// "auth-signing=2/1" (current 2, previous 1 still accepted).
	VersionsName = "MISTY_SERVER_KEY_VERSIONS"
)

// Format is how a derived key is written out.
type Format int

const (
	// Base64 is 32 bytes in standard base64: signing keys and shared secrets.
	Base64 Format = iota
	// Hex is 32 bytes as hex: passwords and tokens that end up in URLs,
	// headers or connection strings.
	Hex
	// OperatorToken is a bearer token with a recognisable prefix.
	OperatorToken
	// Ed25519 is a PKCS#8 Ed25519 private key in standard base64, whose seed
	// is the derived bytes.
	Ed25519
	// AgeKey is an age X25519 identity (the backup key).
	AgeKey
)

// Scope says which secret a key comes from.
type Scope int

const (
	// FromAPISecret keys are the API's: it derives them itself.
	FromAPISecret Scope = iota
	// FromRoot keys come straight from the root. Setup hands them only to the
	// program that needs each one, as a file.
	FromRoot
)

// Key is one derived value.
type Key struct {
	// Name is the environment variable it fills.
	Name string
	// Purpose is its derivation label. Never change one: it would change the key.
	Purpose string
	Format  Format
	Scope   Scope
	// Previous is the slot that keeps the old value valid during rotation.
	// For Ed25519 keys it holds the old public key.
	Previous string
}

// Keys are every key a server stack uses.
var Keys = []Key{
	{Name: "MISTY_AUTH_SIGNING_KEY", Purpose: "auth-signing", Format: Base64, Previous: "MISTY_AUTH_SIGNING_KEY_PREVIOUS"},
	{Name: "MISTY_OPERATOR_TOKEN", Purpose: "operator-token", Format: OperatorToken, Previous: "MISTY_OPERATOR_TOKEN_PREVIOUS"},
	{Name: "MISTY_METRICS_TOKEN", Purpose: "metrics-token", Format: Hex},
	{Name: "SPACE_LINK_ENCRYPTION_KEY", Purpose: "space-link-encryption", Format: Base64},
	{Name: "JOURNAL_COLLAB_TICKET_PRIVATE_KEY", Purpose: "journal-collab-ticket", Format: Ed25519, Previous: "JOURNAL_COLLAB_TICKET_PUBLIC_KEY_PREVIOUS"},
	{Name: "JOURNAL_COLLAB_CONTROL_SECRET", Purpose: "journal-collab-control", Format: Base64, Previous: "JOURNAL_COLLAB_CONTROL_SECRET_PREVIOUS"},
	{Name: "JOURNAL_COLLAB_PROJECTION_SECRET", Purpose: "journal-collab-projection", Format: Base64, Previous: "JOURNAL_COLLAB_PROJECTION_SECRET_PREVIOUS"},
	{Name: "JOURNAL_COLLAB_ROOM_SALT", Purpose: "journal-collab-room-salt", Format: Base64},
	{Name: "DB_PASSWORD", Purpose: "database-app-password", Format: Hex},
	{Name: "DB_MIGRATION_PASSWORD", Purpose: "database-superuser-password", Format: Hex, Scope: FromRoot, Previous: "DB_MIGRATION_PASSWORD_PREVIOUS"},
	{Name: "MISTY_BACKUP_AGE_IDENTITY", Purpose: "backup-age-identity", Format: AgeKey, Scope: FromRoot},
}

// apiSecretPurpose labels the API's share of the root.
const apiSecretPurpose = "api-secret"

// Lookup finds a key by its variable name.
func Lookup(name string) (Key, bool) {
	for _, key := range Keys {
		if key.Name == name {
			return key, true
		}
	}
	return Key{}, false
}

// NewRoot returns a new root secret.
func NewRoot() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(raw), nil
}

// DecodeSecret reads a root or API secret: at least 32 bytes, standard base64.
func DecodeSecret(name, value string) ([]byte, error) {
	secret, err := base64.StdEncoding.DecodeString(strings.TrimSpace(value))
	if err != nil || len(secret) < 32 {
		return nil, fmt.Errorf("%s must be at least 32 base64-encoded random bytes", name)
	}
	return secret, nil
}

// APISecret is the API's share of the root, in standard base64.
func APISecret(root []byte) (string, error) {
	raw, err := derive(root, apiSecretPurpose, 1)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(raw), nil
}

// Source returns the secret a key derives from, given the root. The API,
// which has no root, passes its API secret to Value directly.
func (k Key) Source(root []byte) ([]byte, error) {
	if k.Scope == FromRoot {
		return root, nil
	}
	return derive(root, apiSecretPurpose, 1)
}

// Value derives the key's version from its source secret.
func (k Key) Value(source []byte, version int) (string, error) {
	raw, err := derive(source, k.Purpose, version)
	if err != nil {
		return "", err
	}
	return Encode(k.Format, raw)
}

// PreviousValue is what the key's previous slot holds for a version: the
// value itself, or for Ed25519 keys its public key.
func (k Key) PreviousValue(source []byte, version int) (string, error) {
	value, err := k.Value(source, version)
	if err != nil || k.Format != Ed25519 {
		return value, err
	}
	return Ed25519PublicKey(value)
}

// PreviousOf turns a key's old value into what its previous slot holds.
func (k Key) PreviousOf(value string) (string, error) {
	if k.Format != Ed25519 {
		return value, nil
	}
	return Ed25519PublicKey(value)
}

func derive(secret []byte, purpose string, version int) ([]byte, error) {
	if len(secret) < 32 {
		return nil, errors.New("the secret to derive from is shorter than 32 bytes")
	}
	if version < 1 {
		return nil, fmt.Errorf("key versions start at 1, not %d", version)
	}
	return hkdf.Key(sha256.New, secret, nil, "misty-server/"+purpose+"/v"+strconv.Itoa(version), 32)
}

// Encode writes 32 bytes in a key format.
func Encode(format Format, raw []byte) (string, error) {
	switch format {
	case Base64:
		return base64.StdEncoding.EncodeToString(raw), nil
	case Hex:
		return hex.EncodeToString(raw), nil
	case OperatorToken:
		return "misty_op_" + base64.RawURLEncoding.EncodeToString(raw), nil
	case Ed25519:
		if len(raw) != ed25519.SeedSize {
			return "", errors.New("an Ed25519 seed is 32 bytes")
		}
		der, err := x509.MarshalPKCS8PrivateKey(ed25519.NewKeyFromSeed(raw))
		if err != nil {
			return "", err
		}
		return base64.StdEncoding.EncodeToString(der), nil
	case AgeKey:
		return AgeIdentity(raw)
	}
	return "", fmt.Errorf("unknown key format %d", format)
}

// Ed25519PublicKey is the base64 public key of a base64 PKCS#8 private key.
func Ed25519PublicKey(encoded string) (string, error) {
	der, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		return "", errors.New("must be base64-encoded PKCS#8")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return "", errors.New("must be a PKCS#8 private key")
	}
	key, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return "", errors.New("must be an Ed25519 key")
	}
	return base64.StdEncoding.EncodeToString(key.Public().(ed25519.PublicKey)), nil
}

// Version is one key's current version and the previous one still accepted
// (0 for none).
type Version struct{ Current, Previous int }

// Versions maps a key's purpose to its version. A key that is not listed is
// at version 1 with no previous one.
type Versions map[string]Version

// Of returns a purpose's version.
func (v Versions) Of(purpose string) Version {
	if version, ok := v[purpose]; ok && version.Current >= 1 {
		return version
	}
	return Version{Current: 1}
}

// String writes the versions that differ from the default, sorted, such as
// "auth-signing=2/1,metrics-token=3"; "" when every key is at version 1.
func (v Versions) String() string {
	var parts []string
	for purpose, version := range v {
		switch {
		case version.Current <= 1 && version.Previous == 0:
		case version.Previous > 0:
			parts = append(parts, fmt.Sprintf("%s=%d/%d", purpose, version.Current, version.Previous))
		default:
			parts = append(parts, fmt.Sprintf("%s=%d", purpose, version.Current))
		}
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

// ParseVersions reads what String writes.
func ParseVersions(text string) (Versions, error) {
	versions := Versions{}
	for _, part := range strings.Split(text, ",") {
		if part = strings.TrimSpace(part); part == "" {
			continue
		}
		purpose, value, ok := strings.Cut(part, "=")
		current, previous, hasPrevious := strings.Cut(value, "/")
		version := Version{}
		var err error
		if version.Current, err = strconv.Atoi(current); err != nil || !ok || purpose == "" || version.Current < 1 {
			return nil, fmt.Errorf("%s entries are purpose=version or purpose=version/previous: %q", VersionsName, part)
		}
		if hasPrevious {
			if version.Previous, err = strconv.Atoi(previous); err != nil || version.Previous < 1 || version.Previous >= version.Current {
				return nil, fmt.Errorf("%s: the previous version must be below the current one: %q", VersionsName, part)
			}
		}
		versions[purpose] = version
	}
	return versions, nil
}
