package console

import (
	"errors"
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"

	"github.com/misty-org/misty-server/internal/platform/db"
)

// gigabyte is the decimal gigabyte plans are sold in (50 GB = 50,000,000,000 bytes).
const gigabyte = 1_000_000_000

// limitsFromForm reads the limits form. A blank field is unlimited.
func limitsFromForm(form url.Values) (map[string]any, error) {
	limits := map[string]any{}
	storage := strings.TrimSpace(form.Get("storage_gb"))
	if storage == "" {
		limits["storage_bytes"] = nil
	} else {
		gb, err := strconv.ParseFloat(storage, 64)
		if err != nil || gb < 0 || math.IsInf(gb, 0) || gb > 1e9 {
			return nil, errors.New("Storage must be a number of GB, or blank for unlimited.")
		}
		limits["storage_bytes"] = int64(math.Round(gb * gigabyte))
	}
	for _, field := range []struct{ name, label string }{
		{"devices", "Devices"},
	} {
		value := strings.ReplaceAll(strings.TrimSpace(form.Get(field.name)), ",", "")
		if value == "" {
			limits[field.name] = nil
			continue
		}
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil || n < 0 {
			return nil, errors.New(field.label + " must be a whole number, or blank for unlimited.")
		}
		limits[field.name] = n
	}
	return limits, nil
}

// limitInput is a limit as the form shows it: blank when unlimited.
func limitInput(limit *int64) string {
	if limit == nil {
		return ""
	}
	return strconv.FormatInt(*limit, 10)
}

func storageInput(limit *int64) string {
	if limit == nil {
		return ""
	}
	return strconv.FormatFloat(float64(*limit)/gigabyte, 'f', -1, 64)
}

func gbLabel(n int64) string {
	return strconv.FormatFloat(float64(n)/gigabyte, 'f', 1, 64) + " GB"
}

// usageAgainst is "3 of 5" or "3 · no limit".
func usageAgainst(used int64, limit *int64) string {
	if limit == nil {
		return fmt.Sprintf("%d · no limit", used)
	}
	return fmt.Sprintf("%d of %d", used, *limit)
}

func storageAgainst(used *int64, limit *int64) string {
	text := "not measured"
	if used != nil {
		text = gbLabel(*used)
	}
	if limit == nil {
		return text + " · no limit"
	}
	return text + " of " + gbLabel(*limit)
}

// atLimit marks a row whose use has reached its limit.
func atLimit(used int64, limit *int64) bool { return limit != nil && used >= *limit }

func deviceColumn(a db.OperatorAccount) string {
	if a.Limits.Devices == nil {
		return strconv.FormatInt(a.Usage.Devices, 10)
	}
	return fmt.Sprintf("%d/%d", a.Usage.Devices, *a.Limits.Devices)
}

func setupLabel(a db.OperatorAccount) string {
	switch {
	case a.Setup.Done:
		return "Signed in"
	case a.Setup.LiveKey:
		return "Sign-in link made " + agoPtr(a.Setup.LastSentAt) + ", not used yet"
	case a.Setup.LastSentAt != nil:
		return "Sign-in link expired or replaced; make a new one"
	}
	return "No sign-in link made"
}

// closingLabel says when a closed account is deleted.
func closingLabel(a db.OperatorAccount) string {
	if a.DeleteAt == nil {
		return ""
	}
	return "Closed and read-only: it can sign in and export until " + date(*a.DeleteAt) + ", when its data is deleted unless it is resumed first."
}

// periodLabel names the billing period app actions count in.
func periodLabel(a db.OperatorAccount) string {
	if a.Usage.PeriodStart.IsZero() {
		return "Usage this period"
	}
	return "Usage since " + a.Usage.PeriodStart.UTC().Format("Jan 2")
}
