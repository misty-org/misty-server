package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// The collaboration secrets a value already in the environment keeps.
var journalSecrets = map[string]bool{
	"JOURNAL_COLLAB_TICKET_PRIVATE_KEY": true,
	"JOURNAL_COLLAB_CONTROL_SECRET":     true,
	"JOURNAL_COLLAB_PROJECTION_SECRET":  true,
	"JOURNAL_COLLAB_ROOM_SALT":          true,
}

// loadJournalServerEnv reads the collaboration server's secrets from the file
// the development stack's setup service writes (misty-dev-setup), when
// MISTY_JOURNAL_SERVER_ENV_FILE names one. A secret already set in the
// environment wins over the file.
func loadJournalServerEnv(path string) error {
	if path == "" {
		return nil
	}
	info, err := os.Stat(path)
	if err != nil || info.Size() == 0 {
		return fmt.Errorf("Journal collaboration server secrets are missing at %s.", path)
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	lines := bufio.NewScanner(file)
	for lines.Scan() {
		line := strings.TrimSpace(lines.Text())
		name, value, found := strings.Cut(line, "=")
		if !found || name == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if journalSecrets[name] && os.Getenv(name) != "" {
			continue
		}
		if err := os.Setenv(name, value); err != nil {
			return err
		}
	}
	return lines.Err()
}
