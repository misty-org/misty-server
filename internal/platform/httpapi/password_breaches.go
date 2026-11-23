package api

import (
	"bufio"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/misty-org/misty-server/internal/platform/config"
)

const pwnedPasswordsURL = "https://api.pwnedpasswords.com/range/"

var errPasswordBreached = errors.New("this password has appeared in a data breach; choose a different one")

var pwnedPasswordsClient = &http.Client{Timeout: 3 * time.Second}

func checkNewPasswordBreached(ctx context.Context, password string) error {
	if testing.Testing() || !strings.EqualFold(strings.TrimSpace(config.Getenv("MISTY_ENVIRONMENT")), "production") {
		return nil
	}
	breached, err := pwnedPassword(ctx, password)
	if err != nil {
		log.Printf("leaked password list not reached: %v", err)
		return nil
	}
	if breached {
		return errPasswordBreached
	}
	return nil
}

func pwnedPassword(ctx context.Context, password string) (bool, error) {
	sum := sha1.Sum([]byte(password))
	digest := strings.ToUpper(hex.EncodeToString(sum[:]))
	prefix, suffix := digest[:5], digest[5:]
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, pwnedPasswordsURL+prefix, nil)
	if err != nil {
		return false, err
	}
	request.Header.Set("Add-Padding", "true")
	request.Header.Set("User-Agent", "misty-server")
	response, err := pwnedPasswordsClient.Do(request)
	if err != nil {
		return false, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return false, errors.New("leaked password list: status " + response.Status)
	}
	scanner := bufio.NewScanner(io.LimitReader(response.Body, 4<<20))
	for scanner.Scan() {
		hash, count, found := strings.Cut(strings.TrimSpace(scanner.Text()), ":")
		if found && strings.EqualFold(hash, suffix) && strings.TrimSpace(count) != "0" {
			return true, nil
		}
	}
	return false, scanner.Err()
}
