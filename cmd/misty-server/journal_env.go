package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

var journalSecrets = map[string]bool{
	"JOURNAL_COLLAB_TICKET_PRIVATE_KEY": true,
	"JOURNAL_COLLAB_CONTROL_SECRET":     true,
	"JOURNAL_COLLAB_PROJECTION_SECRET":  true,
	"JOURNAL_COLLAB_ROOM_SALT":          true,
}

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
