package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/misty-org/misty-server/internal/console"
	"github.com/misty-org/misty-server/internal/platform/config"
	"github.com/misty-org/misty-server/internal/platform/db"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:7070", "loopback address to listen on")
	published := flag.String("published", "", "loopback address Compose publishes the console on; lets -addr listen inside a container")
	apiURL := flag.String("api", "http://127.0.0.1:8081", "base URL of the running misty-server")
	handoff := flag.String("handoff", "", "file to write the launch URL to for `misty server up --gui`")
	envSchema := flag.String("env-schema", "", "JSON settings schema written by the CLI for the Configuration page")
	flag.Parse()
	if err := config.ApplyServerDefaults(); err != nil {
		fmt.Fprintf(os.Stderr, "misty-console: %v\n", err)
		os.Exit(1)
	}
	serverDir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "misty-console: %v\n", err)
		os.Exit(1)
	}

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
		PublishedAddr: *published,
		APIURL:        *apiURL,
		MetricsToken:  config.Getenv("MISTY_METRICS_TOKEN"),
		OperatorToken: config.Getenv("MISTY_OPERATOR_TOKEN"),
		Environment:   config.Getenv("MISTY_ENVIRONMENT"),
		HandoffPath:   *handoff,
		ServerDir:     serverDir,
		EnvSchemaPath: *envSchema,
		LookupEnv:     config.LookupEnv,
	}
	if err := console.Run(ctx, cfg, database); err != nil {
		fmt.Fprintf(os.Stderr, "misty-console: %v\n", err)
		os.Exit(1)
	}
}
