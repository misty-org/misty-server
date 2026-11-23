package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/joho/godotenv"

	"github.com/misty-org/misty-server/internal/platform/serverenv"
)

type Store struct {
	Dir    string
	Values map[string]string
	State  map[string]string
}

const (
	envFileName   = "server.env"
	stateFileName = "state.json"
)

const tunnelState = "tunnel-id"

var privateFiles = map[string]string{
	serverenv.RootName:               "server-secret",
	"DB_MIGRATION_PASSWORD":          "postgres/superuser-password",
	"DB_MIGRATION_PASSWORD_PREVIOUS": "postgres/superuser-password-previous",
	"CLOUDFLARE_TUNNEL_TOKEN":        "cloudflared/token",
	"MISTY_BACKUP_AGE_IDENTITY":      "backup/identity",
	"MISTY_BACKUP_R2_ACCESS_KEY":     "backup/r2-access-key",
	"MISTY_BACKUP_R2_SECRET_KEY":     "backup/r2-secret-key",
}

var sharedFiles = map[string]string{
	"DB_PASSWORD":          "postgres/app-password",
	"MISTY_OPERATOR_TOKEN": "operator/token",
	"LIVEKIT_API_SECRET":   "livekit/api-secret",
}

var renderedValues = []string{serverenv.APISecretName, serverenv.VersionsName}

func openStore(dir string) (*Store, error) {
	store := &Store{Dir: dir, Values: map[string]string{}, State: map[string]string{}}
	if state, err := os.ReadFile(filepath.Join(dir, stateFileName)); err == nil {
		if err := json.Unmarshal(state, &store.State); err != nil {
			return nil, fmt.Errorf("%s is damaged: %w", stateFileName, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	data, err := os.ReadFile(store.envPath())
	if errors.Is(err, os.ErrNotExist) {
		data, err = nil, nil
	}
	if err != nil {
		return nil, err
	}
	values, err := godotenv.UnmarshalBytes(data)
	if err != nil {
		return nil, fmt.Errorf("%s is not a valid environment file: %w", store.envPath(), err)
	}
	for name, value := range values {
		if strings.TrimSpace(value) != "" {
			store.Values[name] = value
		}
	}
	for name, file := range privateFiles {
		data, err := os.ReadFile(filepath.Join(dir, file))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if value := strings.TrimSpace(string(data)); value != "" {
			store.Values[name] = value
		}
	}
	store.normalize()
	return store, nil
}

func (s *Store) envPath() string { return filepath.Join(s.Dir, envFileName) }

func (s *Store) EnvMissing() bool {
	info, err := os.Stat(s.envPath())
	return err != nil || info.Size() == 0
}

func (s *Store) Get(name string) string {
	if value := s.Values[name]; value != "" {
		return value
	}
	if value := s.computed(name); value != "" {
		return value
	}
	return s.derived(name)
}

func (s *Store) Has(name string) bool { return strings.TrimSpace(s.Get(name)) != "" }

func (s *Store) Stored(name string) bool { return strings.TrimSpace(s.Values[name]) != "" }

func (s *Store) Set(name, value string) {
	if value == "" {
		delete(s.Values, name)
		return
	}
	s.Values[name] = value
}

func (s *Store) Save() error {
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return err
	}
	env := map[string]string{}
	for name, value := range s.Values {
		if _, private := privateFiles[name]; !private {
			env[name] = value
		}
	}
	for _, name := range renderedValues {
		if value := s.Get(name); value != "" {
			env[name] = value
		}
	}
	var b strings.Builder
	b.WriteString("# Written by the Misty setup container. Do not edit; delete the stack to start over.\n")
	for _, name := range sortedKeys(env) {
		value := env[name]
		if strings.ContainsAny(value, "'\n\r\x00") {
			return fmt.Errorf("%s holds a character the environment file cannot carry", name)
		}
		fmt.Fprintf(&b, "%s='%s'\n", name, value)
	}
	if err := writeAtomic(s.envPath(), []byte(b.String())); err != nil {
		return err
	}
	for _, files := range []map[string]string{privateFiles, sharedFiles} {
		for name, file := range files {
			value := s.Get(name)
			if value == "" {
				if err := os.Remove(filepath.Join(s.Dir, file)); err != nil && !errors.Is(err, os.ErrNotExist) {
					return err
				}
				continue
			}
			if err := writeAtomic(filepath.Join(s.Dir, file), []byte(value+"\n")); err != nil {
				return err
			}
		}
	}
	state, err := json.MarshalIndent(s.State, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(s.Dir, stateFileName), state)
}

func writeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o444); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
