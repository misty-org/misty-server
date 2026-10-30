package app

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/kannachi323/misty/server/internal/platform/config"
)

// validateProductionEnvironment rejects configurations that could let the
// process start successfully while core production features are unusable or
// insecure. Feature-specific constructors perform the deeper key and secret
// validation after this baseline check.
func ValidateProductionEnvironment() error {
	if !strings.EqualFold(strings.TrimSpace(config.Getenv("MISTY_ENVIRONMENT")), "production") {
		return nil
	}
	required := []string{
		"DB_HOST", "DB_USER", "DB_PASSWORD", "DB_NAME", "MISTY_PUBLIC_API_URL",
		"SPACE_LINK_ENCRYPTION_KEY",
	}
	for _, name := range required {
		if strings.TrimSpace(config.Getenv(name)) == "" {
			return fmt.Errorf("%s is required in production", name)
		}
	}
	// Object storage is S3/R2 or a server directory. S3 needs all its keys;
	// a directory needs only its path (checked when the store opens).
	if libraryBackend() == "s3" {
		storage := libraryS3ConfigFromEnv()
		for name, value := range map[string]string{
			"MISTY_S3_ENDPOINT (or R2_ENDPOINT)": storage.endpoint, "MISTY_S3_BUCKET (or R2_BUCKET)": storage.bucket,
			"MISTY_S3_ACCESS_KEY_ID (or R2_ACCESS_KEY)": storage.accessKey, "MISTY_S3_SECRET_ACCESS_KEY (or R2_SECRET_KEY)": storage.secretKey,
		} {
			if value == "" {
				return fmt.Errorf("%s is required in production when Library storage is S3", name)
			}
		}
	} else if libraryBackend() != "filesystem" {
		return fmt.Errorf("production needs Library storage: set MISTY_LIBRARY_FILESYSTEM_DIR or the MISTY_S3_* (R2_*) keys")
	}

	publicURL, err := url.Parse(strings.TrimSpace(config.Getenv("MISTY_PUBLIC_API_URL")))
	if err != nil || publicURL.Scheme != "https" || publicURL.Host == "" {
		return fmt.Errorf("MISTY_PUBLIC_API_URL must be an absolute https URL in production")
	}
	if rawPort := strings.TrimSpace(config.Getenv("PORT")); rawPort != "" {
		port, err := strconv.Atoi(rawPort)
		if err != nil || port < 1 || port > 65535 {
			return fmt.Errorf("PORT must be an integer between 1 and 65535")
		}
	}

	return nil
}
