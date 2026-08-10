package db

import (
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestDatabaseSSLMode(t *testing.T) {
	tests := map[string]string{
		"localhost":           "disable",
		"127.0.0.1":           "disable",
		"::1":                 "disable",
		"postgres":            "disable",
		"/var/run/postgresql": "disable",
		"database.internal":   "require",
		"db.example.com":      "require",
	}
	for host, want := range tests {
		if got := TestingDatabaseSSLMode(host); got != want {
			t.Errorf("databaseSSLMode(%q) = %q, want %q", host, got, want)
		}
	}
}
