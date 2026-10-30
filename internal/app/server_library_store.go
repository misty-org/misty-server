package app

import (
	"fmt"
	"strings"

	envconfig "github.com/kannachi323/misty/server/internal/platform/config"

	api "github.com/kannachi323/misty/server/internal/platform/httpapi"
)

type libraryS3Config struct {
	endpoint, bucket, region, accessKey, secretKey string
	forcePathStyle                                 bool
}

func libraryS3ConfigFromEnv() libraryS3Config {
	config := libraryS3Config{
		endpoint:       firstConfigured("MISTY_S3_ENDPOINT", "R2_ENDPOINT"),
		bucket:         firstConfigured("MISTY_S3_BUCKET", "R2_BUCKET"),
		region:         firstConfigured("MISTY_S3_REGION", "R2_REGION"),
		accessKey:      firstConfigured("MISTY_S3_ACCESS_KEY_ID", "R2_ACCESS_KEY"),
		secretKey:      firstConfigured("MISTY_S3_SECRET_ACCESS_KEY", "R2_SECRET_KEY"),
		forcePathStyle: true,
	}
	if config.region == "" {
		config.region = "auto"
	}
	if value := strings.TrimSpace(envconfig.Getenv("MISTY_S3_FORCE_PATH_STYLE")); value != "" {
		config.forcePathStyle = strings.EqualFold(value, "true") || value == "1"
	}
	return config
}

func (config libraryS3Config) empty() bool {
	return config.endpoint == "" && config.bucket == "" && config.accessKey == "" && config.secretKey == ""
}

// libraryBackend is "filesystem", "s3", or "" when nothing is configured.
// MISTY_LIBRARY_BACKEND wins; otherwise a filesystem directory, then S3 keys.
func libraryBackend() string {
	backend := strings.ToLower(strings.TrimSpace(envconfig.Getenv("MISTY_LIBRARY_BACKEND")))
	if backend == "" {
		if firstConfigured("MISTY_LIBRARY_FILESYSTEM_DIR", "MISTY_LIBRARY_LOCAL_DIR") != "" {
			backend = "filesystem"
		} else if !libraryS3ConfigFromEnv().empty() {
			backend = "s3"
		}
	}
	return backend
}

// LibraryStoreFromEnv opens the object store for Library items, attachments
// and avatars. A self-hosted server can keep them in a directory
// (MISTY_LIBRARY_FILESYSTEM_DIR), in production too; files then flow through
// the API instead of signed S3 URLs.
func LibraryStoreFromEnv() (api.LibraryObjectStore, error) {
	config := libraryS3ConfigFromEnv()
	environment := strings.TrimSpace(envconfig.Getenv("MISTY_ENVIRONMENT"))
	backend := libraryBackend()
	localRoot := firstConfigured("MISTY_LIBRARY_FILESYSTEM_DIR", "MISTY_LIBRARY_LOCAL_DIR")
	if backend == "filesystem" {
		store, err := api.NewLocalLibraryObjectStore(localRoot)
		if err != nil {
			return nil, fmt.Errorf("configure local Library store: %w", err)
		}
		return store, nil
	}
	if backend == "" && !strings.EqualFold(environment, "production") {
		return api.NewMemoryLibraryObjectStore(), nil
	}
	if backend != "s3" {
		return nil, fmt.Errorf("MISTY_LIBRARY_BACKEND must be filesystem or s3")
	}
	if config.endpoint == "" {
		return nil, fmt.Errorf("MISTY_S3_ENDPOINT (or R2_ENDPOINT) is required for the Space Library")
	}
	store, err := api.NewS3LibraryObjectStore(api.S3LibraryObjectStoreConfig{
		Endpoint: config.endpoint, Region: config.region, Bucket: config.bucket,
		AccessKeyID: config.accessKey, SecretAccessKey: config.secretKey,
		ForcePathStyle: config.forcePathStyle, BucketPrivate: true, PermanentObjects: true,
	})
	if err != nil {
		return nil, fmt.Errorf("configure R2 Library store: %w", err)
	}
	return store, nil
}

// libraryStorageName names a Library store for /instance.
func libraryStorageName(store api.LibraryObjectStore) string {
	switch store.(type) {
	case *api.S3LibraryObjectStore:
		return "s3"
	case *api.LocalLibraryObjectStore:
		return "filesystem"
	}
	return "memory"
}

func firstConfigured(names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(envconfig.Getenv(name)); value != "" {
			return value
		}
	}
	return ""
}
