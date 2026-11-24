package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func main() {
	command := "startup"
	if len(os.Args) > 1 {
		command = os.Args[1]
	}
	ctx := context.Background()
	var err error
	switch command {
	case "startup":
		err = startup(ctx)
	case "grant-app-role":
		err = grantAppRole(ctx)
	default:
		fmt.Fprintln(os.Stderr, "Usage: misty-setup [startup|grant-app-role]")
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func startup(ctx context.Context) error {
	steps := []struct {
		name string
		run  func(context.Context) error
	}{
		{"Misty migrations", migrate},
		{"application permissions", grantAppRole},
	}
	for _, step := range steps {
		fmt.Println("Setup: " + step.name)
		if err := step.run(ctx); err != nil {
			return fmt.Errorf("Setup failed during: %s: %w", step.name, err)
		}
	}
	fmt.Println("Server setup complete.")
	return nil
}

func migrate(ctx context.Context) error {
	goose := exec.CommandContext(ctx, "goose", "up")
	goose.Stdout, goose.Stderr = os.Stdout, os.Stderr
	return goose.Run()
}

func required(names ...string) (map[string]string, error) {
	values := map[string]string{}
	for _, name := range names {
		values[name] = os.Getenv(name)
		if values[name] == "" {
			return nil, fmt.Errorf("%s are required", strings.Join(names, " and "))
		}
	}
	return values, nil
}
