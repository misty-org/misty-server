package main

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
)

// provisionWorkers deploys the collaboration Worker and
// records where the API reaches them.
func provisionWorkers(ctx context.Context, cf *cloudflare, inputs Inputs, names Names, store *Store, bundles string) error {
	var account struct {
		Subdomain string `json:"subdomain"`
	}
	if err := cf.call(ctx, "GET", cf.accountPath("workers/subdomain"), nil, &account); err != nil && !notFound(err) {
		return err
	}
	if strings.TrimSpace(account.Subdomain) == "" {
		return errors.New("turn on a workers.dev subdomain for this Cloudflare account (Workers & Pages → Settings) and start again")
	}
	collab, err := loadWorkerBundle(filepath.Join(bundles, "journal-collab"))
	if err != nil {
		return err
	}
	deploys := []workerDeploy{
		{Name: names.CollabWorker, Bundle: collab,
			Vars: map[string]string{"MISTY_INTERNAL_API_BASE": "https://" + inputs.Hostname + "/v1"},
			Secrets: map[string]string{
				"JOURNAL_COLLAB_TICKET_PUBLIC_KEY":          store.Get("JOURNAL_COLLAB_TICKET_PUBLIC_KEY"),
				"JOURNAL_COLLAB_TICKET_PUBLIC_KEY_PREVIOUS": store.Get("JOURNAL_COLLAB_TICKET_PUBLIC_KEY_PREVIOUS"),
				"JOURNAL_COLLAB_CONTROL_SECRET":             store.Get("JOURNAL_COLLAB_CONTROL_SECRET"),
				"JOURNAL_COLLAB_CONTROL_SECRET_PREVIOUS":    store.Get("JOURNAL_COLLAB_CONTROL_SECRET_PREVIOUS"),
				"JOURNAL_COLLAB_PROJECTION_SECRET":          store.Get("JOURNAL_COLLAB_PROJECTION_SECRET"),
			}},
	}
	for _, worker := range deploys {
		if err := deployWorker(ctx, cf, store, worker); err != nil {
			return err
		}
	}
	store.Set("PARTYKIT_HOST", names.CollabWorker+"."+account.Subdomain+".workers.dev")
	return nil
}
