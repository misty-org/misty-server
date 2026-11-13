package db

import (
	"strings"
	"testing"
)

func TestDSNValuesDoNotConsumeFollowingOptions(t *testing.T) {
	for _, password := range []string{"", "spaces and 'quotes' \\ suffix"} {
		t.Setenv("DB_HOST", "/tmp/database socket")
		t.Setenv("DB_PORT", "55473")
		t.Setenv("DB_USER", "test user")
		t.Setenv("DB_PASSWORD", password)
		t.Setenv("DB_NAME", "misty_test")
		dsn := (&Database{}).GetDSN()
		if !strings.Contains(dsn, "password="+quoteDSNValue(password)+" dbname='misty_test'") {
			t.Fatal("DSN does not delimit password and database name")
		}
	}
	if quoteDSNValue("a'b\\c") != "'a\\'b\\\\c'" {
		t.Fatal("PostgreSQL DSN escaping is incorrect")
	}
}
