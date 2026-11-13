package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// writeJournalSecrets hands the API the collaboration server's secrets
// through the runtime volume (/run/misty/journal-server.env), which the API
// reads at start through MISTY_JOURNAL_SERVER_ENV_FILE.
func writeJournalSecrets(context.Context) error {
	runtime := envOr("MISTY_RUNTIME_DIR", "/run/misty")
	if err := os.MkdirAll(runtime, 0o755); err != nil {
		return err
	}
	path := filepath.Join(runtime, "journal-server.env")
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	values, err := required("JOURNAL_COLLAB_TICKET_PRIVATE_KEY", "JOURNAL_COLLAB_CONTROL_SECRET", "JOURNAL_COLLAB_PROJECTION_SECRET")
	if err != nil {
		return errors.New("Journal collaboration secrets are missing or only partially configured.\nRun: misty setup server")
	}
	contents := envFile(values, "JOURNAL_COLLAB_TICKET_PRIVATE_KEY", "JOURNAL_COLLAB_CONTROL_SECRET", "JOURNAL_COLLAB_PROJECTION_SECRET")
	if err := os.WriteFile(path, []byte(contents), 0o444); err != nil {
		return err
	}
	if err := os.Chmod(path, 0o444); err != nil {
		return err
	}
	fmt.Println("Journal collaboration secrets are ready.")
	return nil
}

// deploy uploads the development collaboration Worker with wrangler. Only
// what the Worker needs goes to Cloudflare: never the private signing key.
func deploy(ctx context.Context) error {
	if os.Getenv("CLOUDFLARE_API_TOKEN") == "" {
		return errors.New("CLOUDFLARE_API_TOKEN is required for the containerized Worker deployment.\nAdd a Workers Scripts:Edit API token to ~/.misty/.env/server/cloudflare.env.")
	}
	values, err := required("JOURNAL_COLLAB_TICKET_PUBLIC_KEY", "JOURNAL_COLLAB_CONTROL_SECRET", "JOURNAL_COLLAB_PROJECTION_SECRET")
	if err != nil {
		return errors.New("The Worker secrets are missing from ~/.misty/.env/server/crypto/journal.env.")
	}
	origin := os.Getenv("MISTY_DEV_API_ORIGIN")
	if origin == "" {
		return errors.New("MISTY_DEV_API_ORIGIN: run misty setup cloudflare")
	}
	if !strings.HasPrefix(origin, "https://") {
		return fmt.Errorf("Refusing non-HTTPS development API origin: %s", origin)
	}
	apiBase := strings.TrimSuffix(origin, "/") + "/v1"
	worker := envOr("MISTY_CLOUDFLARE_WORKER_NAME", "misty-journal-collab-dev")

	secrets, err := os.CreateTemp("", "worker-secrets-*")
	if err != nil {
		return err
	}
	defer os.Remove(secrets.Name())
	contents := envFile(values, "JOURNAL_COLLAB_TICKET_PUBLIC_KEY", "JOURNAL_COLLAB_CONTROL_SECRET", "JOURNAL_COLLAB_PROJECTION_SECRET")
	if _, err := secrets.WriteString(contents); err != nil {
		secrets.Close()
		return err
	}
	if err := secrets.Close(); err != nil {
		return err
	}

	fmt.Printf("Deploying %s with callbacks to %s\n", worker, apiBase)
	wrangler := exec.CommandContext(ctx, "npx", "wrangler", "deploy",
		"--name", worker,
		"--var", "MISTY_INTERNAL_API_BASE:"+apiBase,
		"--secrets-file", secrets.Name())
	wrangler.Dir = "/app"
	wrangler.Stdout, wrangler.Stderr = os.Stdout, os.Stderr
	return wrangler.Run()
}

func envFile(values map[string]string, names ...string) string {
	var contents strings.Builder
	for _, name := range names {
		fmt.Fprintf(&contents, "%s=%s\n", name, values[name])
	}
	return contents.String()
}
