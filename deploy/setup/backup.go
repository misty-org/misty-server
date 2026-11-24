package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"hash"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/misty-org/misty-server/internal/platform/config"
)

const (
	backupPrefix = "misty/"
	backupHour   = 3
)

var backupResponseChecksums = []minio.ChecksumType{
	minio.ChecksumCRC64NVME,
	minio.ChecksumCRC32,
	minio.ChecksumCRC32C,
	minio.ChecksumSHA256,
	minio.ChecksumSHA1,
	minio.ChecksumSHA512,
	minio.ChecksumMD5,
	minio.ChecksumXXHash64,
	minio.ChecksumXXHash3,
	minio.ChecksumXXHash128,
}

func backupClient(store *Store) (*minio.Core, string, error) {
	bucket, endpoint := resourceNames(config.Getenv("MISTY_HOSTNAME")).BackupBucket, store.Get("R2_ENDPOINT")
	if strings.TrimSpace(config.Getenv("MISTY_HOSTNAME")) == "" {
		bucket = ""
	}
	access, secret := store.Get("MISTY_BACKUP_R2_ACCESS_KEY"), store.Get("MISTY_BACKUP_R2_SECRET_KEY")
	if bucket == "" || endpoint == "" || access == "" || secret == "" {
		return nil, "", errors.New("the backup bucket is not provisioned yet; the setup service must run first")
	}
	parsed, err := url.Parse(strings.TrimRight(endpoint, "/"))
	if err != nil || parsed.Host == "" {
		return nil, "", fmt.Errorf("the R2 endpoint %q is not a URL", endpoint)
	}
	client, err := minio.NewCore(parsed.Host, &minio.Options{
		Creds:        credentials.NewStaticV4(access, secret, ""),
		Secure:       parsed.Scheme == "https",
		Region:       "auto",
		BucketLookup: minio.BucketLookupPath,
		MaxRetries:   3,
	})
	if err != nil {
		return nil, "", err
	}
	return client, bucket, nil
}

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
	checksum := minio.ChecksumCRC32.Hasher()
	if _, err := io.Copy(checksum, file); err != nil {
		return err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	key := backupPrefix + time.Now().UTC().Format("2006-01-02T15-04-05Z") + ".dump.age"
	if _, err := client.PutObject(ctx, bucket, key, file, info.Size(), "", "", minio.PutObjectOptions{
		UserMetadata:         map[string]string{minio.ChecksumCRC32.Key(): base64.StdEncoding.EncodeToString(checksum.Sum(nil))},
		DisableContentSha256: true,
	}); err != nil {
		return fmt.Errorf("upload %s: %w", key, err)
	}
	log.Printf("backed up %s (%d bytes)", key, info.Size())
	return nil
}

func backupKeys(ctx context.Context, client *minio.Core, bucket string) ([]string, error) {
	var keys []string
	fetchOwner := false
	for object := range client.ListObjectsIter(ctx, bucket, minio.ListObjectsOptions{Prefix: backupPrefix, Recursive: true, FetchOwner: &fetchOwner}) {
		if object.Err != nil {
			return nil, object.Err
		}
		keys = append(keys, object.Key)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	sort.Strings(keys)
	return keys, nil
}

func verifiedBackupBody(body io.ReadCloser, header http.Header) io.ReadCloser {
	var algorithm minio.ChecksumType
	expected := ""
	for _, candidate := range backupResponseChecksums {
		if value := header.Get(candidate.Key()); value != "" {
			algorithm, expected = candidate, value
		}
	}
	if expected == "" || strings.Contains(expected, "-") {
		return body
	}
	return &backupChecksumReader{ReadCloser: body, hash: algorithm.Hasher(), algorithm: algorithm, expected: expected}
}

type backupChecksumReader struct {
	io.ReadCloser
	hash      hash.Hash
	algorithm minio.ChecksumType
	expected  string
}

func (reader *backupChecksumReader) Read(p []byte) (int, error) {
	n, err := reader.ReadCloser.Read(p)
	reader.hash.Write(p[:n])
	if err == io.EOF {
		actual := base64.StdEncoding.EncodeToString(reader.hash.Sum(nil))
		if !strings.EqualFold(reader.expected, actual) {
			return n, fmt.Errorf("checksum did not match: algorithm %s, expect %s, actual %s", reader.algorithm, reader.expected, actual)
		}
	}
	return n, err
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
	options := minio.GetObjectOptions{}
	options.Set("x-amz-checksum-mode", "ENABLED")
	body, _, header, err := client.GetObject(ctx, bucket, key, options)
	if err != nil {
		return fmt.Errorf("download %s: %w", key, err)
	}
	defer body.Close()
	identity := secretsDir() + "/" + privateFiles["MISTY_BACKUP_AGE_IDENTITY"]
	decrypt := exec.CommandContext(ctx, "age", "--decrypt", "--identity", identity)
	decrypt.Stdin, decrypt.Stderr = verifiedBackupBody(body, header), os.Stderr
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

func printBackupKey() error { return printSecret("MISTY_BACKUP_AGE_IDENTITY") }

func printSecret(name string) error {
	store, err := openStore(secretsDir())
	if err != nil {
		return err
	}
	value := store.Get(name)
	if value == "" {
		return fmt.Errorf("%s is missing; start the stack once first", name)
	}
	fmt.Println(value)
	return nil
}
