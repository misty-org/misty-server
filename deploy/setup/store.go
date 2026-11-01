package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/joho/godotenv"
)

// Store is the secrets volume. server.env holds the generated secrets and the
// values derived from Cloudflare that the API and the console read, one
// KEY='value' per line, so both shell entrypoints (". server.env") and Go
// (godotenv) read it. Values the API must never see live only in their own
// files: the database superuser password, the tunnel token and the backup
// key and credentials.
type Store struct {
	Dir    string
	Values map[string]string
	// State is setup's own bookkeeping (state.json), such as the fingerprint
	// of each deployed Worker.
	State map[string]string
}

const (
	envFileName   = "server.env"
	stateFileName = "state.json"
)

// privateFiles are kept out of server.env, each in its own file.
var privateFiles = map[string]string{
	"DB_MIGRATION_PASSWORD":      "postgres/superuser-password",
	"CLOUDFLARE_TUNNEL_TOKEN":    "cloudflared/token",
	"MISTY_BACKUP_AGE_IDENTITY":  "backup/identity",
	"MISTY_BACKUP_R2_ACCESS_KEY": "backup/r2-access-key",
	"MISTY_BACKUP_R2_SECRET_KEY": "backup/r2-secret-key",
}

// sharedFiles are in server.env and also in a file of their own, for
// programs that read *_FILE paths.
var sharedFiles = map[string]string{
	"DB_PASSWORD": "postgres/app-password",
}

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
	return store, nil
}

func (s *Store) envPath() string { return filepath.Join(s.Dir, envFileName) }

// Empty reports whether the volume holds no secrets at all.
func (s *Store) Empty() bool { return len(s.Values) == 0 }

// EnvMissing reports whether server.env is absent or blank, which is what a
// lost or new secrets volume looks like.
func (s *Store) EnvMissing() bool {
	info, err := os.Stat(s.envPath())
	return err != nil || info.Size() == 0
}

func (s *Store) Get(name string) string { return s.Values[name] }

func (s *Store) Has(name string) bool { return strings.TrimSpace(s.Values[name]) != "" }

// Set records a value; an empty value removes the name.
func (s *Store) Set(name, value string) {
	if value == "" {
		delete(s.Values, name)
		return
	}
	s.Values[name] = value
}

// Save writes server.env and the per-value files atomically. Files are
// world-readable inside the volume: only Misty's own containers mount it, and
// they run as different users (postgres, the API, cloudflared).
func (s *Store) Save() error {
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return err
	}
	names := make([]string, 0, len(s.Values))
	for name := range s.Values {
		names = append(names, name)
	}
	sort.Strings(names)
	var b strings.Builder
	b.WriteString("# Written by the Misty setup container. Do not edit; delete the stack to start over.\n")
	for _, name := range names {
		if _, private := privateFiles[name]; private {
			continue
		}
		value := s.Values[name]
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
