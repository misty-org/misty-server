package app

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	envconfig "github.com/kannachi323/misty/server/internal/platform/config"
)

// validateProductionEnvironment rejects configurations that could let the
// process start successfully while core production features are unusable or
// insecure. Feature-specific constructors perform the deeper key and secret
// validation after this baseline check.
func TestingValidateProductionEnvironment() error {
	if !strings.EqualFold(strings.TrimSpace(envconfig.Getenv("MISTY_ENVIRONMENT")), "production") {
		return nil
	}
	selfHosted := strings.EqualFold(strings.TrimSpace(envconfig.Getenv("MISTY_DEPLOYMENT_MODE")), "self_hosted")

	var required []string
	if selfHosted {
		required = []string{
			"DB_HOST", "DB_USER", "DB_PASSWORD", "DB_NAME", "MISTY_PUBLIC_API_URL",
			"SPACE_LINK_ENCRYPTION_KEY", "MISTY_INSTANCE_NAME", "MISTY_LIBRARY_BACKEND",
			"PARTYKIT_HOST", "JOURNAL_COLLAB_TICKET_PRIVATE_KEY", "JOURNAL_COLLAB_CONTROL_SECRET",
			"JOURNAL_COLLAB_PROJECTION_SECRET", "JOURNAL_COLLAB_ROOM_SALT", "MISTY_COLLAB_INTERNAL_SECRET",
			"MISTY_COLLAB_PUBLIC_URL",
		}
		switch strings.ToLower(strings.TrimSpace(envconfig.Getenv("MISTY_LIBRARY_BACKEND"))) {
		case "filesystem":
			required = append(required, "MISTY_LIBRARY_FILESYSTEM_DIR")
		case "s3":
			required = append(required, "MISTY_S3_ENDPOINT", "MISTY_S3_BUCKET", "MISTY_S3_REGION", "MISTY_S3_ACCESS_KEY_ID", "MISTY_S3_SECRET_ACCESS_KEY")
		}
	} else {
		required = []string{
			"R2_ENDPOINT", "R2_BUCKET", "R2_ACCESS_KEY", "R2_SECRET_KEY",
			"DB_HOST", "DB_USER", "DB_PASSWORD", "DB_NAME", "MISTY_PUBLIC_API_URL",
			"MISTY_OPERATOR_USER_ID", "STRIPE_SECRET_KEY", "STRIPE_WEBHOOK_SECRET",
			"STRIPE_PRICE_PRO_MONTHLY", "STRIPE_PRICE_PRO_YEARLY",
			"STRIPE_PRICE_MAX_MONTHLY", "STRIPE_PRICE_MAX_YEARLY",
			"STRIPE_CHECKOUT_SUCCESS_URL", "STRIPE_CHECKOUT_CANCEL_URL", "STRIPE_PORTAL_RETURN_URL",
			"MISTY_SELF_HOST_ENTITLEMENT_PRIVATE_KEY", "MISTY_SELF_HOST_ENTITLEMENT_KEY_ID",
			"MISTY_SELF_HOST_ENTITLEMENT_SUBJECT_SECRET",
			"SPACE_LINK_ENCRYPTION_KEY",
		}
	}
	for _, name := range required {
		if strings.TrimSpace(envconfig.Getenv(name)) == "" {
			return fmt.Errorf("%s is required in production", name)
		}
	}

	publicURL, err := url.Parse(strings.TrimSpace(envconfig.Getenv("MISTY_PUBLIC_API_URL")))
	if err != nil || publicURL.Scheme != "https" || publicURL.Host == "" {
		return fmt.Errorf("MISTY_PUBLIC_API_URL must be an absolute https URL in production")
	}
	if !selfHosted && len(strings.TrimSpace(envconfig.Getenv("STRIPE_WEBHOOK_SECRET"))) < 16 {
		return fmt.Errorf("STRIPE_WEBHOOK_SECRET is too short for production")
	}
	for _, name := range []string{
		"STRIPE_CHECKOUT_SUCCESS_URL",
		"STRIPE_CHECKOUT_CANCEL_URL",
		"STRIPE_PORTAL_RETURN_URL",
	} {
		if selfHosted {
			break
		}
		value, parseErr := url.Parse(strings.TrimSpace(envconfig.Getenv(name)))
		if parseErr != nil || value.Scheme != "https" || value.Host == "" {
			return fmt.Errorf("%s must be an absolute https URL in production", name)
		}
	}
	priceIDs := map[string]string{}
	for _, name := range []string{
		"STRIPE_PRICE_PRO_MONTHLY",
		"STRIPE_PRICE_PRO_YEARLY",
		"STRIPE_PRICE_MAX_MONTHLY",
		"STRIPE_PRICE_MAX_YEARLY",
	} {
		if selfHosted {
			break
		}
		priceID := strings.TrimSpace(envconfig.Getenv(name))
		if previous, exists := priceIDs[priceID]; exists {
			return fmt.Errorf("%s and %s must use different Stripe Price IDs", previous, name)
		}
		priceIDs[priceID] = name
	}

	if rawPort := strings.TrimSpace(envconfig.Getenv("PORT")); rawPort != "" {
		port, err := strconv.Atoi(rawPort)
		if err != nil || port < 1 || port > 65535 {
			return fmt.Errorf("PORT must be an integer between 1 and 65535")
		}
	}

	return nil
}
