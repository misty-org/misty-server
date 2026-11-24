package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/misty-org/misty-server/internal/platform/config"
	"github.com/misty-org/misty-server/internal/platform/serverenv"
)

func secretsDir() string { return envOr("MISTY_SECRETS_DIR", "/run/misty/secrets") }

func main() {
	log.SetFlags(0)
	log.SetPrefix("misty-setup: ")
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	args := os.Args[1:]
	command := "init"
	if len(args) > 0 {
		command, args = args[0], args[1:]
	}
	var err error
	switch command {
	case "init":
		err = runInit(ctx)
	case "rotate", "retire", "adopt-root":
		err = runRotate(ctx, command, args)
	case "migrate":
		err = runMigrate(ctx)
	case "backup":
		err = runBackups(ctx, len(args) > 0 && args[0] == "--now")
	case "backups":
		err = listBackups(ctx)
	case "restore":
		if len(args) != 1 {
			err = fmt.Errorf("restore needs one backup name, or latest")
		} else {
			err = restoreBackup(ctx, args[0])
		}
	case "backup-key":
		err = printBackupKey()
	case "operator-token":
		err = printSecret("MISTY_OPERATOR_TOKEN")
	case "watch":
		err = runWatch(ctx)
	default:
		err = fmt.Errorf("unknown command %q", command)
	}
	if err != nil {
		log.Fatal(err)
	}
}

func runInit(ctx context.Context) error {
	inputs, err := readInputs(config.Getenv)
	if err != nil {
		return err
	}
	store, err := openStore(secretsDir())
	if err != nil {
		return err
	}
	if err := guardData(store, databaseHasData(config.Getenv("MISTY_DATA_DIR"))); err != nil {
		return err
	}
	made, err := ensureRoot(store)
	if err != nil {
		return err
	}
	if err := store.Save(); err != nil {
		return err
	}
	if made {
		log.Printf("generated %s; keep a copy of the secrets volume off the server", serverenv.RootName)
	}
	if stored := storedKeys(store); len(stored) > 0 {
		log.Printf("%d keys keep values stored before %s (%s); adopt-root moves them onto it", len(stored), serverenv.RootName, strings.Join(stored, ", "))
	}
	return provision(ctx, inputs, store)
}

const (
	zoneState    = "cloudflare-zone"
	accountState = "cloudflare-account"
)

func provision(ctx context.Context, inputs Inputs, store *Store) error {
	resolved := inputs
	if err := provisionCloudflare(ctx, &resolved, store); err != nil {
		if store.State["provisioned"] == inputsFingerprint(recordedZone(inputs, store)) && store.Has("CLOUDFLARE_TUNNEL_TOKEN") {
			log.Printf("WARNING: Cloudflare is unreachable or refused a change (%v); starting with the resources from the last run", err)
			return nil
		}
		return err
	}
	store.State["provisioned"] = inputsFingerprint(resolved)
	store.State[zoneState], store.State[accountState] = resolved.ZoneID, resolved.AccountID
	if err := store.Save(); err != nil {
		return err
	}
	log.Printf("ready: https://%s", inputs.Hostname)
	return nil
}

func recordedZone(inputs Inputs, store *Store) Inputs {
	if inputs.ZoneID == "" {
		inputs.ZoneID = store.State[zoneState]
	}
	if inputs.AccountID == "" {
		inputs.AccountID = store.State[accountState]
	}
	return inputs
}

func provisionCloudflare(ctx context.Context, inputs *Inputs, store *Store) error {
	cf := newCloudflare(*inputs)
	names := resourceNames(inputs.Hostname)
	zone, err := resolveZone(ctx, cf, inputs)
	if err != nil {
		return err
	}
	if err := provisionTunnel(ctx, cf, *inputs, zone, names, store); err != nil {
		return err
	}
	if err := provisionR2(ctx, cf, *inputs, names, store); err != nil {
		return err
	}
	if endpoint := config.Getenv("MISTY_R2_ENDPOINT_URL"); endpoint != "" {
		store.Set("R2_ENDPOINT", endpoint)
	}
	return store.Save()
}

func inputsFingerprint(inputs Inputs) string {
	parts := append([]string{inputs.Hostname, inputs.AccountID, inputs.ZoneID, inputs.APIURL}, inputs.Origins...)
	for _, rule := range inputs.ExtraIngress {
		parts = append(parts, "ingress "+rule.Host+"="+rule.Service)
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return hex.EncodeToString(sum[:])
}

func runRotate(ctx context.Context, command string, names []string) error {
	discardData := false
	if command == "adopt-root" {
		for _, arg := range names {
			if arg != "--discard-data" {
				return fmt.Errorf("adopt-root takes no names, only --discard-data")
			}
			discardData = true
		}
	} else if len(names) == 0 {
		return fmt.Errorf("%s needs at least one secret name", command)
	}
	store, err := openStore(secretsDir())
	if err != nil {
		return err
	}
	if store.EnvMissing() {
		return fmt.Errorf("the secrets volume is empty; start the stack once first")
	}
	if command == "adopt-root" {
		moved, kept, err := adoptRoot(store, discardData)
		if err != nil {
			return err
		}
		names = moved
		if len(kept) > 0 {
			log.Printf("kept %s: they protect stored data; adopt-root --discard-data moves them too, making that data unreadable", strings.Join(kept, ", "))
		}
		if len(moved) == 0 {
			log.Printf("every key already comes from %s", serverenv.RootName)
			return nil
		}
	}
	for _, name := range names {
		switch command {
		case "rotate":
			err = rotate(store, name)
		case "retire":
			err = retire(store, name)
		}
		if err != nil {
			return err
		}
	}
	if err := store.Save(); err != nil {
		return err
	}
	verb := map[string]string{"rotate": "rotated", "retire": "retired", "adopt-root": "moved onto the root"}[command]
	log.Printf("%s %s; run docker compose up -d --force-recreate to apply", verb, strings.Join(names, ", "))
	inputs, err := readInputs(config.Getenv)
	if err != nil {
		return err
	}
	return provision(ctx, inputs, store)
}
