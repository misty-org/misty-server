// Package config is the only production boundary that reads process
// environment variables. Domain and adapter packages depend on this boundary
// instead of coupling themselves directly to the operating system.
package config

import (
	"os"
	"strings"
)

const defaultPort = "8080"

// Runtime contains the process-level values needed to start the HTTP server.
// It is loaded once after optional dotenv files have been applied.
type Runtime struct {
	Port string
}

// LoadRuntime returns a validated, immutable runtime configuration value.
func LoadRuntime() Runtime {
	port := strings.TrimSpace(Getenv("PORT"))
	if port == "" {
		port = defaultPort
	}
	return Runtime{Port: port}
}

// Getenv provides the compatibility boundary for adapters whose constructors
// have not yet accepted a narrower typed configuration value.
func Getenv(name string) string {
	return os.Getenv(name)
}

// LookupEnv reports whether an environment value is explicitly present.
func LookupEnv(name string) (string, bool) {
	return os.LookupEnv(name)
}
