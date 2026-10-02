// Package console serves the operator console: a loopback-only web GUI for
// inspecting and administering a Misty server. It runs as its own process
// (cmd/misty-console) so none of its handlers are reachable through the
// public API or the Cloudflare tunnel.
package console

import (
	"errors"
	"fmt"
	"net"
	"strings"
)

// Config holds everything the console needs; cmd/misty-console reads it from
// flags and the environment so this package never touches the process env.
type Config struct {
	// Addr must resolve to a loopback interface, e.g. 127.0.0.1:7070.
	Addr string
	// APIURL is the base URL of the running misty-server, without a path.
	APIURL string
	// MetricsToken is MISTY_METRICS_TOKEN; empty disables metrics scraping.
	MetricsToken string
	// Environment is MISTY_ENVIRONMENT, e.g. development or production.
	Environment string
	// HandoffPath is where the launch URL is written for `misty server up --gui`.
	HandoffPath string
	// ServerDir is the server checkout, used for docker compose (Services)
	// and to list .misty/backups (Database).
	ServerDir string
	// EnvSchemaPath is a JSON list of known variables written by the CLI for
	// the Configuration page; empty hides the inventory.
	EnvSchemaPath string
	// LookupEnv reads the console process environment. It is injected so this
	// package never reads the environment directly.
	LookupEnv func(string) (string, bool)
}

func (c Config) lookup(name string) (string, bool) {
	if c.LookupEnv == nil {
		return "", false
	}
	return c.LookupEnv(name)
}

// Development reports whether dev-only pages (such as Services) are available.
func (c Config) Development() bool {
	return strings.EqualFold(c.Environment, "development")
}

// EnvironmentLabel is the short description shown under each page title.
func (c Config) EnvironmentLabel() string {
	if c.Environment == "" {
		return "unknown environment"
	}
	return c.Environment
}

// ValidateLoopback rejects any listen address that is not a loopback
// interface. The console has no network-grade authentication, so binding to a
// routable interface would expose account administration.
func ValidateLoopback(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("invalid listen address %q: %w", addr, err)
	}
	if host == "localhost" {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return errors.New("the console only listens on a loopback address such as 127.0.0.1")
	}
	return nil
}
