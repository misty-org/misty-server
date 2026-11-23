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
	RootName      = "MISTY_SERVER_SECRET"
	APISecretName = "MISTY_API_SECRET"
	VersionsName  = "MISTY_SERVER_KEY_VERSIONS"
)

type Format int

const (
	Base64 Format = iota
	Hex
	OperatorToken
	Ed25519
	AgeKey
)

type Scope int

const (
	FromAPISecret Scope = iota
	FromRoot
)

type Key struct {
	Name     string
	Purpose  string
	Format   Format
	Scope    Scope
	Previous string
}

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
	{Name: "LIVEKIT_API_SECRET", Purpose: "livekit-api-secret", Format: Hex},
}

const apiSecretPurpose = "api-secret"

func Lookup(name string) (Key, bool) {
	for _, key := range Keys {
		if key.Name == name {
			return key, true
		}
	}
	return Key{}, false
}

func NewRoot() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(raw), nil
}

func DecodeSecret(name, value string) ([]byte, error) {
	secret, err := base64.StdEncoding.DecodeString(strings.TrimSpace(value))
	if err != nil || len(secret) < 32 {
		return nil, fmt.Errorf("%s must be at least 32 base64-encoded random bytes", name)
	}
	return secret, nil
}

func APISecret(root []byte) (string, error) {
	raw, err := derive(root, apiSecretPurpose, 1)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(raw), nil
}

func (k Key) Source(root []byte) ([]byte, error) {
	if k.Scope == FromRoot {
		return root, nil
	}
	return derive(root, apiSecretPurpose, 1)
}

func (k Key) Value(source []byte, version int) (string, error) {
	raw, err := derive(source, k.Purpose, version)
	if err != nil {
		return "", err
	}
	return Encode(k.Format, raw)
}

func (k Key) PreviousValue(source []byte, version int) (string, error) {
	value, err := k.Value(source, version)
	if err != nil || k.Format != Ed25519 {
		return value, err
	}
	return Ed25519PublicKey(value)
}

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

type Version struct{ Current, Previous int }

type Versions map[string]Version

func (v Versions) Of(purpose string) Version {
	if version, ok := v[purpose]; ok && version.Current >= 1 {
		return version
	}
	return Version{Current: 1}
}

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
