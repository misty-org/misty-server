package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
)

// The Library bucket holds uploads, attachments and avatars; the backup
// bucket holds encrypted database dumps under a lock rule, so a token that
// leaks from this server cannot delete or overwrite a backup.

const (
	backupRetentionDays = 30
	r2WritePermission   = "Workers R2 Storage Bucket Item Write"
)

// desktopOrigins are the app's own origins, which upload to the Library
// bucket with signed URLs.
var desktopOrigins = []string{"tauri://localhost", "http://tauri.localhost", "https://tauri.localhost"}

func provisionR2(ctx context.Context, cf *cloudflare, inputs Inputs, names Names, store *Store) error {
	for _, bucket := range []string{names.LibraryBucket, names.BackupBucket} {
		if err := ensureBucket(ctx, cf, bucket); err != nil {
			return err
		}
	}
	origins := append(append([]string{}, desktopOrigins...), inputs.Origins...)
	cors := map[string]any{"rules": []map[string]any{{
		"allowed": map[string]any{
			"origins": origins,
			"methods": []string{"GET", "HEAD", "PUT"},
			"headers": []string{"content-type", "x-amz-checksum-sha256", "x-amz-meta-misty-library-sha256"},
		},
		"exposeHeaders": []string{"etag", "x-amz-checksum-sha256"},
		"maxAgeSeconds": 3600,
	}}}
	if err := cf.call(ctx, "PUT", cf.accountPath("r2/buckets/%s/cors", names.LibraryBucket), cors, nil); err != nil {
		return err
	}
	retention := backupRetentionDays * 24 * 60 * 60
	lock := map[string]any{"rules": []map[string]any{{
		"id": "misty-backups", "enabled": true, "prefix": "",
		"condition": map[string]any{"type": "Age", "maxAgeSeconds": retention},
	}}}
	if err := cf.call(ctx, "PUT", cf.accountPath("r2/buckets/%s/lock", names.BackupBucket), lock, nil); err != nil {
		return err
	}
	// Delete a day after the lock lets go, so the bucket does not grow forever.
	lifecycle := map[string]any{"rules": []map[string]any{{
		"id": "misty-backups-expire", "enabled": true, "conditions": map[string]any{"prefix": ""},
		"deleteObjectsTransition": map[string]any{"condition": map[string]any{"type": "Age", "maxAge": retention + 24*60*60}},
	}}}
	if err := cf.call(ctx, "PUT", cf.accountPath("r2/buckets/%s/lifecycle", names.BackupBucket), lifecycle, nil); err != nil {
		return err
	}

	permission, err := r2PermissionGroup(ctx, cf)
	if err != nil {
		return err
	}
	if err := ensureBucketToken(ctx, cf, store, names.LibraryToken, names.LibraryBucket, permission, "R2_ACCESS_KEY", "R2_SECRET_KEY"); err != nil {
		return err
	}
	if err := ensureBucketToken(ctx, cf, store, names.BackupToken, names.BackupBucket, permission, "MISTY_BACKUP_R2_ACCESS_KEY", "MISTY_BACKUP_R2_SECRET_KEY"); err != nil {
		return err
	}
	endpoint := "https://" + inputs.AccountID + ".r2.cloudflarestorage.com"
	store.Set("R2_ENDPOINT", endpoint)
	store.Set("R2_BUCKET", names.LibraryBucket)
	store.Set("MISTY_BACKUP_BUCKET", names.BackupBucket)
	return nil
}

func ensureBucket(ctx context.Context, cf *cloudflare, name string) error {
	err := cf.call(ctx, "GET", cf.accountPath("r2/buckets/%s", name), nil, nil)
	if notFound(err) {
		return cf.call(ctx, "POST", cf.accountPath("r2/buckets"), map[string]any{"name": name}, nil)
	}
	return err
}

func r2PermissionGroup(ctx context.Context, cf *cloudflare) (string, error) {
	var groups []struct{ ID, Name string }
	if err := cf.call(ctx, "GET", cf.accountPath("tokens/permission_groups"), nil, &groups); err != nil {
		return "", err
	}
	for _, group := range groups {
		if group.Name == r2WritePermission {
			return group.ID, nil
		}
	}
	return "", fmt.Errorf("Cloudflare has no %q permission group for this account", r2WritePermission)
}

type apiToken struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Value string `json:"value"`
}

// ensureBucketToken keeps an account API token that can read and write one
// bucket, and its S3 credentials (access key = token ID, secret = SHA-256 of
// the token value). A stored pair is kept while its token exists; a token
// whose value was lost has its value rolled instead of creating another.
func ensureBucketToken(ctx context.Context, cf *cloudflare, store *Store, tokenName, bucket, permission, accessName, secretName string) error {
	if id := store.Get(accessName); id != "" && store.Has(secretName) {
		err := cf.call(ctx, "GET", cf.accountPath("tokens/%s", id), nil, nil)
		if err == nil {
			return nil
		}
		if !notFound(err) {
			return err
		}
	}
	var tokens []apiToken
	if err := cf.call(ctx, "GET", cf.accountPath("tokens?per_page=100"), nil, &tokens); err != nil {
		return err
	}
	var token apiToken
	for _, existing := range tokens {
		if existing.Name == tokenName {
			var value string
			if err := cf.call(ctx, "PUT", cf.accountPath("tokens/%s/value", existing.ID), map[string]any{}, &value); err != nil {
				return err
			}
			token = apiToken{ID: existing.ID, Name: existing.Name, Value: value}
			break
		}
	}
	if token.ID == "" {
		body := map[string]any{"name": tokenName, "policies": []map[string]any{{
			"effect":            "allow",
			"resources":         map[string]string{"com.cloudflare.edge.r2.bucket." + cf.account + "_default_" + bucket: "*"},
			"permission_groups": []map[string]string{{"id": permission}},
		}}}
		if err := cf.call(ctx, "POST", cf.accountPath("tokens"), body, &token); err != nil {
			return err
		}
	}
	if token.ID == "" || token.Value == "" {
		return errors.New("Cloudflare returned an R2 token without its value")
	}
	sum := sha256.Sum256([]byte(token.Value))
	store.Set(accessName, token.ID)
	store.Set(secretName, hex.EncodeToString(sum[:]))
	return nil
}
