// Command misty-setup prepares and maintains a Misty server stack
// (server/compose.yml). Each Compose service that is not the API runs one of
// its commands:
//
//	init (default)  generate missing secrets, refuse lost ones, provision Cloudflare
//	rotate NAME…    replace secrets, keeping each old value in its *_PREVIOUS slot
//	retire NAME…    clear the *_PREVIOUS slots once old values are out of use
//	migrate         create the database roles, apply migrations, grant the API role
//	backup [--now]  nightly encrypted database backups to R2
//	backups         list the backups
//	restore KEY     restore one backup ("latest" for the newest)
//	backup-key      print the backup key, to keep a copy off the server
//	watch           email the operator when the server is down or failing
//
// It never changes stored data: secrets that protect data are generated once
// and never replaced.
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
	case "rotate", "retire":
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
	generated, err := generateMissing(store)
	if err != nil {
		return err
	}
	deriveConfig(store, inputs)
	// Keep the secrets before any network call, so a failed Cloudflare step
	// never loses them.
	if err := store.Save(); err != nil {
		return err
	}
	if len(generated) > 0 {
		log.Printf("generated %s", strings.Join(generated, ", "))
	}
	return provision(ctx, inputs, store)
}

// provision makes the Cloudflare resources match the inputs. When Cloudflare
// cannot be reached but an earlier run already provisioned these same inputs,
// the stack starts on the recorded values instead of staying down.
func provision(ctx context.Context, inputs Inputs, store *Store) error {
	fingerprint := inputsFingerprint(inputs)
	if err := provisionCloudflare(ctx, inputs, store); err != nil {
		if store.State["provisioned"] == fingerprint && store.Has("CLOUDFLARE_TUNNEL_TOKEN") && store.Has("PARTYKIT_HOST") {
			log.Printf("WARNING: Cloudflare is unreachable or refused a change (%v); starting with the resources from the last run", err)
			return nil
		}
		return err
	}
	store.State["provisioned"] = fingerprint
	if err := store.Save(); err != nil {
		return err
	}
	log.Printf("ready: https://%s", inputs.Hostname)
	return nil
}

func provisionCloudflare(ctx context.Context, inputs Inputs, store *Store) error {
	cf := newCloudflare(inputs)
	names := resourceNames(inputs.Hostname)
	if err := provisionTunnel(ctx, cf, inputs, names, store); err != nil {
		return err
	}
	if err := provisionR2(ctx, cf, inputs, names, store); err != nil {
		return err
	}
	if endpoint := config.Getenv("MISTY_R2_ENDPOINT_URL"); endpoint != "" {
		// Tests only: an S3 stand-in instead of R2.
		store.Set("R2_ENDPOINT", endpoint)
	}
	if err := store.Save(); err != nil {
		return err
	}
	return provisionWorkers(ctx, cf, inputs, names, store, envOr("MISTY_WORKERS_DIR", "/opt/misty/workers"))
}

func inputsFingerprint(inputs Inputs) string {
	sum := sha256.Sum256([]byte(strings.Join(append([]string{inputs.Hostname, inputs.AccountID, inputs.ZoneID, inputs.APIURL}, inputs.Origins...), "\n")))
	return hex.EncodeToString(sum[:])
}

func runRotate(ctx context.Context, command string, names []string) error {
	if len(names) == 0 {
		return fmt.Errorf("%s needs at least one secret name", command)
	}
	store, err := openStore(secretsDir())
	if err != nil {
		return err
	}
	if store.EnvMissing() {
		return fmt.Errorf("the secrets volume is empty; start the stack once first")
	}
	for _, name := range names {
		if command == "rotate" {
			err = rotate(store, name)
		} else {
			err = retire(store, name)
		}
		if err != nil {
			return err
		}
	}
	if err := store.Save(); err != nil {
		return err
	}
	log.Printf("%sd %s; run docker compose up -d --force-recreate to apply", command, strings.Join(names, ", "))
	// Push the new values to the Workers now.
	inputs, err := readInputs(config.Getenv)
	if err != nil {
		return err
	}
	return provision(ctx, inputs, store)
}
