// Command misty-setup prepares and maintains a Misty server stack
// (compose.yml). Each Compose service that is not the API runs one of
// its commands:
//
//	init (default)  make the root secret, refuse lost secrets, provision Cloudflare
//	rotate NAME…    replace keys, keeping each old value in its *_PREVIOUS slot
//	retire NAME…    clear the *_PREVIOUS slots once old values are out of use
//	adopt-root [--discard-data]
//	                move keys a volume from before the root stores on their own onto the root
//	operator-token  print the operator token, for a service that calls the operator API
//	migrate         create the database roles, apply migrations, grant the API role
//	backup [--now]  nightly encrypted database backups to R2
//	backups         list the backups
//	restore KEY     restore one backup ("latest" for the newest)
//	backup-key      print the backup key, to keep a copy off the server
//	watch           record an alert when the server is down or failing
//
// It never changes stored data: keys that protect data keep their value
// unless the operator discards that data.
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
	// Keep the secrets before any network call, so a failed Cloudflare step
	// never loses them.
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

// State entries for the zone and account setup found, so the outage
// fallback knows them without asking Cloudflare.
const (
	zoneState    = "cloudflare-zone"
	accountState = "cloudflare-account"
)

// provision makes the Cloudflare resources match the inputs. When Cloudflare
// cannot be reached but an earlier run already provisioned these same inputs,
// the stack starts on the recorded values instead of staying down.
func provision(ctx context.Context, inputs Inputs, store *Store) error {
	resolved := inputs
	if err := provisionCloudflare(ctx, &resolved, store); err != nil {
		if store.State["provisioned"] == inputsFingerprint(recordedZone(inputs, store)) && store.Has("CLOUDFLARE_TUNNEL_TOKEN") && store.Has("PARTYKIT_HOST") {
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

// recordedZone fills the zone and account the operator left out with the
// ones the last run found.
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
		// Tests only: an S3 stand-in instead of R2.
		store.Set("R2_ENDPOINT", endpoint)
	}
	if err := store.Save(); err != nil {
		return err
	}
	return provisionWorkers(ctx, cf, *inputs, names, store, envOr("MISTY_WORKERS_DIR", "/opt/misty/workers"))
}

func inputsFingerprint(inputs Inputs) string {
	parts := append([]string{inputs.Hostname, inputs.AccountID, inputs.ZoneID, inputs.APIURL}, inputs.Origins...)
	// Extra hostnames join the fingerprint only when set, so a server without
	// them keeps the fingerprint it already recorded.
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
	// Push the new values to the Workers now.
	inputs, err := readInputs(config.Getenv)
	if err != nil {
		return err
	}
	return provision(ctx, inputs, store)
}
