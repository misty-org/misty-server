package console

import (
	"errors"
	"fmt"
	"net"
	"strings"
)

type Config struct {
	Addr          string
	PublishedAddr string
	APIURL        string
	MetricsToken  string
	OperatorToken string
	Environment   string
	HandoffPath   string
	ServerDir     string
	EnvSchemaPath string
	LookupEnv     func(string) (string, bool)
}

func (c Config) lookup(name string) (string, bool) {
	if c.LookupEnv == nil {
		return "", false
	}
	return c.LookupEnv(name)
}

func (c Config) Development() bool {
	return strings.EqualFold(c.Environment, "development")
}

func (c Config) EnvironmentLabel() string {
	if c.Environment == "" {
		return "unknown environment"
	}
	return c.Environment
}

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
