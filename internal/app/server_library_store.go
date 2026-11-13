package app

import (
	"fmt"
	"strings"

	envconfig "github.com/misty-org/misty-server/internal/platform/config"

	api "github.com/misty-org/misty-server/internal/platform/httpapi"
)

// libraryR2Names are the R2 settings, all required in production. The setup
// container provisions the bucket and its credentials.
var libraryR2Names = []string{"R2_ENDPOINT", "R2_BUCKET", "R2_ACCESS_KEY", "R2_SECRET_KEY"}

func libraryR2Configured() bool {
	for _, name := range libraryR2Names {
		if strings.TrimSpace(envconfig.Getenv(name)) != "" {
			return true
		}
	}
	return false
}

// LibraryStoreFromEnv opens the R2 bucket for Library items, attachments and
// avatars. A development server without R2 keeps them in memory.
func LibraryStoreFromEnv() (api.LibraryObjectStore, error) {
	if !libraryR2Configured() && !productionEnvironment() {
		return api.NewMemoryLibraryObjectStore(), nil
	}
	for _, name := range libraryR2Names {
		if strings.TrimSpace(envconfig.Getenv(name)) == "" {
			return nil, fmt.Errorf("%s is required for Library storage", name)
		}
	}
	store, err := api.NewS3LibraryObjectStore(api.S3LibraryObjectStoreConfig{
		Endpoint: strings.TrimSpace(envconfig.Getenv("R2_ENDPOINT")), Region: "auto",
		Bucket:      strings.TrimSpace(envconfig.Getenv("R2_BUCKET")),
		AccessKeyID: strings.TrimSpace(envconfig.Getenv("R2_ACCESS_KEY")), SecretAccessKey: strings.TrimSpace(envconfig.Getenv("R2_SECRET_KEY")),
		ForcePathStyle: true, BucketPrivate: true, PermanentObjects: true,
	})
	if err != nil {
		return nil, fmt.Errorf("configure R2 Library store: %w", err)
	}
	return store, nil
}
