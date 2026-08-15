package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	envconfig "github.com/kannachi323/misty/server/internal/platform/config"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"github.com/kannachi323/misty/server/internal/platform/security"
)

func main() {
	if !strings.EqualFold(strings.TrimSpace(envconfig.Getenv("MISTY_DEPLOYMENT_MODE")), "self_hosted") {
		fatal("misty-admin is available only when MISTY_DEPLOYMENT_MODE=self_hosted")
	}
	if len(os.Args) < 2 {
		usage()
	}
	database := &db.Database{}
	if err := database.Start(); err != nil {
		fatal("connect to database: %v", err)
	}
	defer database.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	switch os.Args[1] {
	case "bootstrap-token":
		bootstrapToken(ctx, database)
	case "reset-password":
		resetPassword(ctx, database, os.Args[2:])
	case "disable-account":
		disableAccount(ctx, database, os.Args[2:])
	default:
		usage()
	}
}

func bootstrapToken(ctx context.Context, database *db.Database) {
	token, err := security.GenerateSecureToken()
	if err != nil {
		fatal("generate bootstrap token: %v", err)
	}
	expiresAt := time.Now().UTC().Add(30 * time.Minute)
	if err := database.CreateSelfHostBootstrapToken(ctx, security.HashToken(token), expiresAt); err != nil {
		if errors.Is(err, db.ErrSelfHostBootstrapInvalid) {
			fatal("this instance has already been bootstrapped")
		}
		fatal("store bootstrap token: %v", err)
	}
	fmt.Printf("Bootstrap token (single use, expires %s):\n%s\n", expiresAt.Format(time.RFC3339), token)
}

func resetPassword(ctx context.Context, database *db.Database, args []string) {
	flags := flag.NewFlagSet("reset-password", flag.ExitOnError)
	email := flags.String("email", "", "self-hosted account email")
	_ = flags.Parse(args)
	if strings.TrimSpace(*email) == "" {
		fatal("--email is required")
	}
	fmt.Fprint(os.Stderr, "New password (read from stdin): ")
	password, err := bufio.NewReader(os.Stdin).ReadString('\n')
	password = strings.TrimSpace(password)
	if err != nil && password == "" {
		fatal("read password: %v", err)
	}
	if len(password) < 8 {
		fatal("password must contain at least 8 characters")
	}
	if err := database.ResetSelfHostPassword(ctx, *email, password); err != nil {
		fatal("reset password: %v", err)
	}
	fmt.Println("Password reset and existing sessions revoked.")
}

func disableAccount(ctx context.Context, database *db.Database, args []string) {
	flags := flag.NewFlagSet("disable-account", flag.ExitOnError)
	email := flags.String("email", "", "self-hosted account email")
	_ = flags.Parse(args)
	if strings.TrimSpace(*email) == "" {
		fatal("--email is required")
	}
	if err := database.DisableSelfHostAccount(ctx, *email); err != nil {
		fatal("disable account: %v", err)
	}
	fmt.Println("Account disabled and all sessions revoked.")
}

func usage() {
	fmt.Fprintln(os.Stderr, "Usage: misty-admin bootstrap-token | reset-password --email EMAIL | disable-account --email EMAIL")
	os.Exit(2)
}

func fatal(format string, values ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", values...)
	os.Exit(1)
}
