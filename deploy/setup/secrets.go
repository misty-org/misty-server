package main

import (
	"strconv"
	"strings"

	"github.com/misty-org/misty-server/internal/platform/serverenv"
)

type secretSpec struct {
	serverenv.Key
	DataBound bool
	Guarded   bool
	Rotatable bool
}

var secretSpecs = []secretSpec{
	{Key: key("DB_MIGRATION_PASSWORD"), Guarded: true},
	{Key: key("DB_PASSWORD"), Rotatable: true},
	{Key: key("MISTY_AUTH_SIGNING_KEY")},
	{Key: key("MISTY_OPERATOR_TOKEN")},
	{Key: key("MISTY_METRICS_TOKEN"), Rotatable: true},
	{Key: key("LIVEKIT_API_SECRET"), Rotatable: true},
	{Key: key("SPACE_LINK_ENCRYPTION_KEY"), DataBound: true},
	{Key: key("MISTY_BACKUP_AGE_IDENTITY"), DataBound: true},
}

var retiredSecrets = []string{
	"DOCUMENT_SIGNING_KEY",
	"JOURNAL_COLLAB_TICKET_PRIVATE_KEY",
	"JOURNAL_COLLAB_TICKET_PUBLIC_KEY_PREVIOUS",
	"JOURNAL_COLLAB_CONTROL_SECRET",
	"JOURNAL_COLLAB_CONTROL_SECRET_PREVIOUS",
	"JOURNAL_COLLAB_PROJECTION_SECRET",
	"JOURNAL_COLLAB_PROJECTION_SECRET_PREVIOUS",
	"JOURNAL_COLLAB_ROOM_SALT",
}

var retiredValues = []string{"PARTYKIT_HOST", "JOURNAL_COLLAB_TICKET_PUBLIC_KEY"}

func key(name string) serverenv.Key {
	found, ok := serverenv.Lookup(name)
	if !ok {
		panic("unknown key " + name)
	}
	return found
}

func specFor(name string) (secretSpec, bool) {
	for _, spec := range secretSpecs {
		if spec.Name == name {
			return spec, true
		}
	}
	return secretSpec{}, false
}

func specForPrevious(name string) (secretSpec, bool) {
	for _, spec := range secretSpecs {
		if spec.Previous != "" && spec.Previous == name {
			return spec, true
		}
	}
	return secretSpec{}, false
}

func versionState(purpose string) string  { return "key:" + purpose + ":version" }
func previousState(purpose string) string { return "key:" + purpose + ":previous" }
func sourceState(purpose string) string   { return "key:" + purpose + ":source" }

const (
	sourceStored = "stored"
	sourceRoot   = "root"
)

func (s *Store) version(purpose string) serverenv.Version {
	current, _ := strconv.Atoi(s.State[versionState(purpose)])
	previous, _ := strconv.Atoi(s.State[previousState(purpose)])
	if current < 1 {
		current = 1
	}
	if previous >= current {
		previous = 0
	}
	return serverenv.Version{Current: current, Previous: previous}
}

func (s *Store) setVersion(purpose string, version serverenv.Version) {
	delete(s.State, versionState(purpose))
	delete(s.State, previousState(purpose))
	if version.Current > 1 {
		s.State[versionState(purpose)] = strconv.Itoa(version.Current)
	}
	if version.Previous > 0 {
		s.State[previousState(purpose)] = strconv.Itoa(version.Previous)
	}
}

func (s *Store) versions() serverenv.Versions {
	versions := serverenv.Versions{}
	for _, spec := range secretSpecs {
		versions[spec.Purpose] = s.version(spec.Purpose)
	}
	return versions
}

func (s *Store) root() []byte {
	encoded := s.Values[serverenv.RootName]
	if encoded == "" {
		return nil
	}
	root, err := serverenv.DecodeSecret(serverenv.RootName, encoded)
	if err != nil {
		return nil
	}
	return root
}

func (s *Store) derived(name string) string {
	root := s.root()
	if root == nil {
		return ""
	}
	value, err := s.deriveFrom(root, name)
	if err != nil {
		return ""
	}
	return value
}

func (s *Store) deriveFrom(root []byte, name string) (string, error) {
	switch name {
	case serverenv.APISecretName:
		return serverenv.APISecret(root)
	case serverenv.VersionsName:
		return s.versions().String(), nil
	}
	if spec, ok := specFor(name); ok {
		source, err := spec.Source(root)
		if err != nil {
			return "", err
		}
		return spec.Value(source, s.version(spec.Purpose).Current)
	}
	if spec, ok := specForPrevious(name); ok {
		previous := s.version(spec.Purpose).Previous
		if previous == 0 {
			return "", nil
		}
		source, err := spec.Source(root)
		if err != nil {
			return "", err
		}
		return spec.PreviousValue(source, previous)
	}
	return "", nil
}

var computedValues = []string{
	"MISTY_BACKUP_AGE_RECIPIENT",
	"MISTY_PUBLIC_API_URL", "R2_BUCKET", "MISTY_BACKUP_BUCKET", "MISTY_CLOUDFLARE_TUNNEL_ID",
	serverenv.APISecretName, serverenv.VersionsName,
}

func (s *Store) computed(name string) string {
	switch name {
	case "MISTY_BACKUP_AGE_RECIPIENT":
		recipient, _ := serverenv.AgeRecipient(s.Get("MISTY_BACKUP_AGE_IDENTITY"))
		return recipient
	}
	return ""
}

func (s *Store) normalize() {
	if id := s.Values["MISTY_CLOUDFLARE_TUNNEL_ID"]; id != "" && s.State[tunnelState] == "" {
		s.State[tunnelState] = id
	}
	for _, name := range append(computedValues, retiredValues...) {
		delete(s.Values, name)
	}
	for name := range s.State {
		if strings.HasPrefix(name, "worker:") {
			delete(s.State, name)
		}
	}
	for _, spec := range secretSpecs {
		for _, name := range []string{spec.Name, spec.Previous} {
			if name != "" && s.Values[name] != "" && s.Values[name] == s.derived(name) {
				delete(s.Values, name)
			}
		}
	}
}
