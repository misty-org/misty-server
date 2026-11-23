package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/misty-org/misty-server/internal/platform/serverenv"
)

func databaseHasData(dataDir string) bool {
	if dataDir == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(dataDir, "PG_VERSION"))
	return err == nil
}

var errSecretsLost = errors.New("secrets lost")

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
		if !spec.DataBound && !spec.Guarded {
			continue
		}
		stored := store.Stored(spec.Name)
		switch store.State[sourceState(spec.Purpose)] {
		case sourceRoot:
			if store.root() == nil {
				missing = append(missing, serverenv.RootName)
			}
		case sourceStored:
			if !stored {
				missing = append(missing, spec.Name)
			}
		default:
			if !stored {
				missing = append(missing, spec.Name)
			}
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("%w: the database holds data but the secrets volume is missing %s. "+
			"Restore the secrets volume from your copy; a new value would make existing data unreadable",
			errSecretsLost, strings.Join(dedupe(missing), ", "))
	}
	return nil
}

func dedupe(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	return out
}

func ensureRoot(store *Store) (bool, error) {
	made := false
	if !store.Stored(serverenv.RootName) {
		root, err := serverenv.NewRoot()
		if err != nil {
			return false, fmt.Errorf("generate %s: %w", serverenv.RootName, err)
		}
		store.Set(serverenv.RootName, root)
		made = true
	} else if store.root() == nil {
		return false, fmt.Errorf("%s in the secrets volume is damaged; restore the volume from your copy", serverenv.RootName)
	}
	for _, spec := range secretSpecs {
		if (spec.DataBound || spec.Guarded) && store.State[sourceState(spec.Purpose)] == "" {
			source := sourceRoot
			if store.Stored(spec.Name) {
				source = sourceStored
			}
			store.State[sourceState(spec.Purpose)] = source
		}
	}
	return made, nil
}

func storedKeys(store *Store) []string {
	var names []string
	for _, spec := range secretSpecs {
		if store.Stored(spec.Name) {
			names = append(names, spec.Name)
		}
	}
	return names
}

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
	if store.root() == nil {
		return fmt.Errorf("the secrets volume has no %s yet; start the stack once first", serverenv.RootName)
	}
	version := store.version(spec.Purpose)
	if old := store.Values[name]; old != "" {
		delete(store.Values, name)
		version.Previous = 0
		if spec.Previous != "" {
			previous, err := spec.PreviousOf(old)
			if err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
			store.Set(spec.Previous, previous)
		}
	} else {
		version = serverenv.Version{Current: version.Current + 1}
		if spec.Previous != "" {
			version.Previous = version.Current - 1
			store.Set(spec.Previous, "")
		}
	}
	store.setVersion(spec.Purpose, version)
	if spec.Guarded {
		store.State[sourceState(spec.Purpose)] = sourceRoot
	}
	return nil
}

func retire(store *Store, name string) error {
	spec, ok := specFor(name)
	if !ok || spec.Previous == "" {
		return fmt.Errorf("%s has no previous slot", name)
	}
	store.Set(spec.Previous, "")
	version := store.version(spec.Purpose)
	version.Previous = 0
	store.setVersion(spec.Purpose, version)
	return nil
}

func adoptRoot(store *Store, discardData bool) (moved, kept []string, err error) {
	if store.root() == nil {
		return nil, nil, fmt.Errorf("the secrets volume has no %s yet; start the stack once first", serverenv.RootName)
	}
	for _, spec := range secretSpecs {
		if !store.Stored(spec.Name) {
			continue
		}
		if spec.DataBound {
			if !discardData {
				kept = append(kept, spec.Name)
				continue
			}
			store.Set(spec.Name, "")
			store.State[sourceState(spec.Purpose)] = sourceRoot
		} else if err := rotate(store, spec.Name); err != nil {
			return nil, nil, err
		}
		moved = append(moved, spec.Name)
	}
	for _, name := range retiredSecrets {
		if store.Stored(name) {
			store.Set(name, "")
			moved = append(moved, name)
		}
	}
	return moved, kept, nil
}
