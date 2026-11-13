package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// Backups are pg_dump custom-format dumps encrypted to the stack's age key and
// uploaded to the locked backup bucket. Plaintext never touches the disk: the
// dump streams through age into a temporary ciphertext file.

const (
	backupPrefix = "misty/"
	backupHour   = 3 // UTC
)

func backupClient(store *Store) (*s3.Client, string, error) {
	bucket, endpoint := store.Get("MISTY_BACKUP_BUCKET"), store.Get("R2_ENDPOINT")
	access, secret := store.Get("MISTY_BACKUP_R2_ACCESS_KEY"), store.Get("MISTY_BACKUP_R2_SECRET_KEY")
	if bucket == "" || endpoint == "" || access == "" || secret == "" {
		return nil, "", errors.New("the backup bucket is not provisioned yet; the setup service must run first")
	}
	client := s3.New(s3.Options{
		Region:       "auto",
		BaseEndpoint: aws.String(endpoint),
		Credentials:  credentials.NewStaticCredentialsProvider(access, secret, ""),
		UsePathStyle: true,
	})
	return client, bucket, nil
}

// runBackups backs up once with --now, otherwise every night at 03:00 UTC.
// A failed night is logged and retried an hour later.
func runBackups(ctx context.Context, now bool) error {
	if now {
		return backupOnce(ctx)
	}
	for {
		next := nextBackup(time.Now().UTC())
		log.Printf("next backup at %s", next.Format(time.RFC3339))
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(time.Until(next)):
		}
		if err := backupOnce(ctx); err != nil {
			log.Printf("backup failed: %v; retrying in an hour", err)
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(time.Hour):
			}
			if err := backupOnce(ctx); err != nil {
				log.Printf("backup failed again: %v", err)
			}
		}
	}
}

func nextBackup(now time.Time) time.Time {
	next := time.Date(now.Year(), now.Month(), now.Day(), backupHour, 0, 0, 0, time.UTC)
	if !next.After(now) {
		next = next.Add(24 * time.Hour)
	}
	return next
}

func backupOnce(ctx context.Context) error {
	store, err := openStore(secretsDir())
	if err != nil {
		return err
	}
	client, bucket, err := backupClient(store)
	if err != nil {
		return err
	}
	recipient := store.Get("MISTY_BACKUP_AGE_RECIPIENT")
	if recipient == "" {
		return errors.New("the backup key is missing")
	}
	file, err := os.CreateTemp("", "misty-backup-*.age")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()

	dump := exec.CommandContext(ctx, "pg_dump", "--format=custom", "--host="+envOr("MISTY_DB_HOST", dbHost), "--username="+dbSuperuser, "--dbname="+dbName)
	dump.Env = append(os.Environ(), "PGPASSWORD="+store.Get("DB_MIGRATION_PASSWORD"))
	dump.Stderr = os.Stderr
	encrypt := exec.CommandContext(ctx, "age", "--encrypt", "--recipient", recipient)
	encrypt.Stdout, encrypt.Stderr = file, os.Stderr
	pipe, err := dump.StdoutPipe()
	if err != nil {
		return err
	}
	encrypt.Stdin = pipe
	if err := encrypt.Start(); err != nil {
		return err
	}
	if err := dump.Run(); err != nil {
		_ = encrypt.Wait()
		return fmt.Errorf("pg_dump: %w", err)
	}
	if err := encrypt.Wait(); err != nil {
		return fmt.Errorf("age: %w", err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	info, err := file.Stat()
	if err != nil {
		return err
	}
	key := backupPrefix + time.Now().UTC().Format("2006-01-02T15-04-05Z") + ".dump.age"
	if _, err := client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucket), Key: aws.String(key), Body: file, ContentLength: aws.Int64(info.Size()),
	}); err != nil {
		return fmt.Errorf("upload %s: %w", key, err)
	}
	log.Printf("backed up %s (%d bytes)", key, info.Size())
	return nil
}

func backupKeys(ctx context.Context, client *s3.Client, bucket string) ([]string, error) {
	var keys []string
	paginator := s3.NewListObjectsV2Paginator(client, &s3.ListObjectsV2Input{Bucket: aws.String(bucket), Prefix: aws.String(backupPrefix)})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, object := range page.Contents {
			keys = append(keys, aws.ToString(object.Key))
		}
	}
	sort.Strings(keys)
	return keys, nil
}

func listBackups(ctx context.Context) error {
	store, err := openStore(secretsDir())
	if err != nil {
		return err
	}
	client, bucket, err := backupClient(store)
	if err != nil {
		return err
	}
	keys, err := backupKeys(ctx, client, bucket)
	if err != nil {
		return err
	}
	for _, key := range keys {
		fmt.Println(strings.TrimPrefix(key, backupPrefix))
	}
	return nil
}

// restoreBackup replaces the database's contents with one backup. Stop the
// API first (docker compose stop api); the next `up` migrates and grants again.
func restoreBackup(ctx context.Context, name string) error {
	store, err := openStore(secretsDir())
	if err != nil {
		return err
	}
	client, bucket, err := backupClient(store)
	if err != nil {
		return err
	}
	key := backupPrefix + strings.TrimPrefix(name, backupPrefix)
	if name == "latest" {
		keys, err := backupKeys(ctx, client, bucket)
		if err != nil {
			return err
		}
		if len(keys) == 0 {
			return errors.New("the backup bucket is empty")
		}
		key = keys[len(keys)-1]
	}
	object, err := client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
	if err != nil {
		return fmt.Errorf("download %s: %w", key, err)
	}
	defer object.Body.Close()
	identity := secretsDir() + "/" + privateFiles["MISTY_BACKUP_AGE_IDENTITY"]
	decrypt := exec.CommandContext(ctx, "age", "--decrypt", "--identity", identity)
	decrypt.Stdin, decrypt.Stderr = object.Body, os.Stderr
	restore := exec.CommandContext(ctx, "pg_restore", "--clean", "--if-exists", "--no-owner", "--single-transaction",
		"--host="+envOr("MISTY_DB_HOST", dbHost), "--username="+dbSuperuser, "--dbname="+dbName)
	restore.Env = append(os.Environ(), "PGPASSWORD="+store.Get("DB_MIGRATION_PASSWORD"))
	restore.Stdout, restore.Stderr = os.Stdout, os.Stderr
	pipe, err := decrypt.StdoutPipe()
	if err != nil {
		return err
	}
	restore.Stdin = pipe
	if err := restore.Start(); err != nil {
		return err
	}
	if err := decrypt.Run(); err != nil {
		_ = restore.Wait()
		return fmt.Errorf("age: %w", err)
	}
	if err := restore.Wait(); err != nil {
		return fmt.Errorf("pg_restore: %w", err)
	}
	log.Printf("restored %s; start the stack again with docker compose up -d", key)
	return nil
}

func printBackupKey() error {
	store, err := openStore(secretsDir())
	if err != nil {
		return err
	}
	identity := store.Get("MISTY_BACKUP_AGE_IDENTITY")
	if identity == "" {
		return errors.New("the backup key is missing")
	}
	fmt.Println(identity)
	return nil
}
