package config

import (
	"os"
	"strings"

	"github.com/misty-org/misty-server/internal/platform/serverenv"
)

const defaultPort = "8080"

type Runtime struct {
	Port string
}

func LoadRuntime() Runtime {
	port := strings.TrimSpace(Getenv("PORT"))
	if port == "" {
		port = defaultPort
	}
	return Runtime{Port: port}
}

func Getenv(name string) string {
	return os.Getenv(name)
}

func LookupEnv(name string) (string, bool) {
	return os.LookupEnv(name)
}

func ApplyServerDefaults() error {
	values, err := serverenv.Defaults(os.Getenv)
	if err != nil {
		return err
	}
	for name, value := range values {
		if err := os.Setenv(name, value); err != nil {
			return err
		}
	}
	return nil
}
