// Command misty-console serves the loopback-only operator console. Start it
// with `misty server up --gui`, which supplies the environment and opens the browser.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/kannachi323/misty/server/internal/console"
	envconfig "github.com/kannachi323/misty/server/internal/platform/config"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:7070", "loopback address to listen on")
	apiURL := flag.String("api", "http://127.0.0.1:8081", "base URL of the running misty-server")
	handoff := flag.String("handoff", "", "file to write the launch URL to for `misty server up --gui`")
	envSchema := flag.String("env-schema", "", "JSON settings schema written by the CLI for the Configuration page")
	flag.Parse()
	serverDir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "misty-console: %v\n", err)
		os.Exit(1)
	}

	// The console stays useful when Postgres is down: the Overview reports the
	// database as unreachable instead of the process refusing to start.
	database := &db.Database{}
	if err := database.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "misty-console: database unavailable: %v\n", err)
		database = nil
	} else {
		defer database.Stop()
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	cfg := console.Config{
		Addr:          *addr,
		APIURL:        *apiURL,
		MetricsToken:  envconfig.Getenv("MISTY_METRICS_TOKEN"),
		Environment:   envconfig.Getenv("MISTY_ENVIRONMENT"),
		HandoffPath:   *handoff,
		ServerDir:     serverDir,
		EnvSchemaPath: *envSchema,
		LookupEnv:     envconfig.LookupEnv,
	}
	if err := console.Run(ctx, cfg, database); err != nil {
		fmt.Fprintf(os.Stderr, "misty-console: %v\n", err)
		os.Exit(1)
	}
}
