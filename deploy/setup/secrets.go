package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type secretKind int

const (
	// randomKey is 32 random bytes, standard base64: signing keys, peppers.
	randomKey secretKind = iota
	// hexSecret is 32 random bytes as hex: passwords and shared secrets that
	// end up in URLs, headers or connection strings.
	hexSecret
	// operatorToken is a bearer token with a recognisable prefix.
	operatorToken
	// ed25519Key is a PKCS#8 Ed25519 private key, standard base64.
	ed25519Key
	// ageIdentity is an age X25519 identity (the backup key).
	ageIdentity
)

// secretSpec is one generated secret.
type secretSpec struct {
	Name string
	Kind secretKind
	// DataBound secrets protect data already written: a new value would make
	// that data unreadable, so they are never regenerated over a database
	// that holds data and never rotated.
	DataBound bool
	// Previous is the slot that keeps the old value valid during rotation.
	// For Ed25519 keys it holds the old public key.
	Previous string
	// Rotatable without a previous slot: the new value takes effect when the
	// stack restarts (the setup run redeploys the Workers that share it).
	Rotatable bool
}

var secretSpecs = []secretSpec{
	{Name: "DB_MIGRATION_PASSWORD", Kind: hexSecret, DataBound: true},
	{Name: "DB_PASSWORD", Kind: hexSecret, Rotatable: true},
	{Name: "MISTY_AUTH_SIGNING_KEY", Kind: randomKey, Previous: "MISTY_AUTH_SIGNING_KEY_PREVIOUS"},
	{Name: "MISTY_OPERATOR_TOKEN", Kind: operatorToken, Previous: "MISTY_OPERATOR_TOKEN_PREVIOUS"},
	{Name: "MISTY_METRICS_TOKEN", Kind: hexSecret, Rotatable: true},
	{Name: "SPACE_LINK_ENCRYPTION_KEY", Kind: randomKey, DataBound: true},
	{Name: "DOCUMENT_SIGNING_KEY", Kind: randomKey, DataBound: true},
	{Name: "JOURNAL_COLLAB_TICKET_PRIVATE_KEY", Kind: ed25519Key, Previous: "JOURNAL_COLLAB_TICKET_PUBLIC_KEY_PREVIOUS"},
	{Name: "JOURNAL_COLLAB_CONTROL_SECRET", Kind: randomKey, Previous: "JOURNAL_COLLAB_CONTROL_SECRET_PREVIOUS"},
	{Name: "JOURNAL_COLLAB_PROJECTION_SECRET", Kind: randomKey, Previous: "JOURNAL_COLLAB_PROJECTION_SECRET_PREVIOUS"},
	{Name: "JOURNAL_COLLAB_ROOM_SALT", Kind: randomKey, DataBound: true},
	{Name: "MISTY_BACKUP_AGE_IDENTITY", Kind: ageIdentity, DataBound: true},
}

func specFor(name string) (secretSpec, bool) {
	for _, spec := range secretSpecs {
		if spec.Name == name {
			return spec, true
		}
	}
	return secretSpec{}, false
}

// databaseHasData reports whether the PostgreSQL data volume was initialised.
func databaseHasData(dataDir string) bool {
	if dataDir == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(dataDir, "PG_VERSION"))
	return err == nil
}

// errSecretsLost stops the stack: the database holds data its secrets no
// longer match.
var errSecretsLost = errors.New("secrets lost")

// guardData refuses to continue when the database holds data but the secrets
// that protect it are gone. Generating new ones would lock the database (its
// passwords) and make encrypted data unreadable.
func guardData(store *Store, hasData bool) error {
	if !hasData {
		return nil
	}
	if store.EnvMissing() {
		return fmt.Errorf("%w: the database volume holds data but the secrets volume is empty. "+
			"Restore the secrets volume from your copy, or delete the whole stack "+
			"(docker compose down --volumes) to start over", errSecretsLost)
	}
	var missing []string
	for _, spec := range secretSpecs {
		if spec.DataBound && !store.Has(spec.Name) {
			missing = append(missing, spec.Name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("%w: the database holds data but the secrets volume is missing %s. "+
			"Restore the secrets volume from your copy; a new value would make existing data unreadable",
			errSecretsLost, strings.Join(missing, ", "))
	}
	return nil
}

// generateMissing fills every secret that has no value and never touches one
// that has. It returns the names it generated.
func generateMissing(store *Store) ([]string, error) {
	var generated []string
	for _, spec := range secretSpecs {
		if store.Has(spec.Name) {
			continue
		}
		value, err := newSecret(spec.Kind)
		if err != nil {
			return nil, fmt.Errorf("generate %s: %w", spec.Name, err)
		}
		store.Set(spec.Name, value)
		generated = append(generated, spec.Name)
	}
	return generated, derivePublicValues(store)
}

// derivePublicValues writes the public halves the Workers and backups need.
func derivePublicValues(store *Store) error {
	public, err := ed25519PublicKey(store.Get("JOURNAL_COLLAB_TICKET_PRIVATE_KEY"))
	if err != nil {
		return fmt.Errorf("JOURNAL_COLLAB_TICKET_PRIVATE_KEY: %w", err)
	}
	store.Set("JOURNAL_COLLAB_TICKET_PUBLIC_KEY", public)
	recipient, err := ageRecipient(store.Get("MISTY_BACKUP_AGE_IDENTITY"))
	if err != nil {
		return fmt.Errorf("MISTY_BACKUP_AGE_IDENTITY: %w", err)
	}
	store.Set("MISTY_BACKUP_AGE_RECIPIENT", recipient)
	return nil
}

// rotate replaces a secret, keeping the old value in its previous slot when it
// has one. Data-bound secrets refuse: rotating them would rewrite data.
func rotate(store *Store, name string) error {
	spec, ok := specFor(name)
	if !ok {
		return fmt.Errorf("%s is not a secret the setup container generates", name)
	}
	if spec.DataBound {
		return fmt.Errorf("%s protects stored data and cannot be rotated without rewriting it", name)
	}
	if spec.Previous == "" && !spec.Rotatable {
		return fmt.Errorf("%s cannot be rotated", name)
	}
	old := store.Get(name)
	value, err := newSecret(spec.Kind)
	if err != nil {
		return err
	}
	if spec.Previous != "" && old != "" {
		previous := old
		if spec.Kind == ed25519Key {
			if previous, err = ed25519PublicKey(old); err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
		}
		store.Set(spec.Previous, previous)
	}
	store.Set(name, value)
	return derivePublicValues(store)
}

// retire clears a previous slot once nothing signed with the old value is
// still in use.
func retire(store *Store, name string) error {
	spec, ok := specFor(name)
	if !ok || spec.Previous == "" {
		return fmt.Errorf("%s has no previous slot", name)
	}
	store.Set(spec.Previous, "")
	return nil
}

func newSecret(kind secretKind) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	switch kind {
	case randomKey:
		return base64.StdEncoding.EncodeToString(raw), nil
	case hexSecret:
		return hex.EncodeToString(raw), nil
	case operatorToken:
		return "misty_op_" + base64.RawURLEncoding.EncodeToString(raw), nil
	case ed25519Key:
		key := ed25519.NewKeyFromSeed(raw)
		der, err := x509.MarshalPKCS8PrivateKey(key)
		if err != nil {
			return "", err
		}
		return base64.StdEncoding.EncodeToString(der), nil
	case ageIdentity:
		return newAgeIdentity(raw)
	}
	return "", fmt.Errorf("unknown secret kind %d", kind)
}

func ed25519PublicKey(encoded string) (string, error) {
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
