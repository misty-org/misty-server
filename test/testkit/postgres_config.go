package testkit

import (
	"fmt"
	"strings"
	"testing"

	envconfig "github.com/misty-org/misty-server/internal/platform/config"
)

func loadDatabaseConfig(t testing.TB) databaseConfig {
	t.Helper()
	config := databaseConfig{
		host: testDatabaseHost(),
		port: firstConfigured("TEST_DB_PORT", "DB_PORT"),
		name: testDatabaseName(),
	}
	config.user, config.password = testDatabaseRole()
	if config.port == "" {
		config.port = "5432"
	}
	config.sslmode = testDatabaseSSLMode(config.host)

	switch {
	case config.host == "":
		t.Fatal(missingDatabaseEnvironment("host", "TEST_DB_HOST or DB_HOST"))
	case config.user == "":
		t.Fatal(missingDatabaseEnvironment("user", "TEST_DB_USER, DB_MIGRATION_USER, or DB_USER"))
	case config.password == "":
		t.Fatal(missingDatabaseEnvironment("password", "TEST_DB_PASSWORD, DB_MIGRATION_PASSWORD, or DB_PASSWORD"))
	case config.name == "":
		t.Fatal(missingDatabaseEnvironment("name", "TEST_DB_NAME or DB_NAME"))
	}
	if !strings.Contains(strings.ToLower(config.name), "test") {
		t.Fatalf("refusing to reset non-test database %q", config.name)
	}
	return config
}

func testDatabaseHost() string {
	host := firstConfigured("TEST_DB_HOST", "DB_HOST")
	switch host {
	case "localhost", "::1":
		return "127.0.0.1"
	}
	return host
}

func testDatabaseName() string {
	if name := configured("TEST_DB_NAME"); name != "" {
		return name
	}
	name := configured("DB_NAME")
	if name == "" || strings.Contains(strings.ToLower(name), "test") {
		return name
	}
	return name + "_test"
}

func testDatabaseRole() (user, password string) {
	if user := configured("TEST_DB_USER"); user != "" {
		return user, configured("TEST_DB_PASSWORD")
	}
	if user := configured("DB_MIGRATION_USER"); user != "" {
		return user, firstConfigured("DB_MIGRATION_PASSWORD", "DB_PASSWORD")
	}
	return configured("DB_USER"), configured("DB_PASSWORD")
}

func testDatabaseSSLMode(host string) string {
	if mode := configured("TEST_DB_SSLMODE"); mode != "" {
		return mode
	}
	if host == "127.0.0.1" || host == "localhost" || host == "::1" {
		return "disable"
	}
	if mode := configured("DB_SSLMODE"); mode != "" {
		return mode
	}
	return "disable"
}

func missingDatabaseEnvironment(field, names string) string {
	return fmt.Sprintf(
		"missing integration DB %s; set %s, or run misty server test to bootstrap the test database",
		field, names,
	)
}

func configured(name string) string {
	return strings.TrimSpace(envconfig.Getenv(name))
}

func firstConfigured(names ...string) string {
	for _, name := range names {
		if value := configured(name); value != "" {
			return value
		}
	}
	return ""
}

func (config databaseConfig) dsn() string {
	return fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		config.host, config.port, config.user, config.password, config.name, config.sslmode,
	)
}
