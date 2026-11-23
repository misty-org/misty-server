package console

import (
	"errors"
	"math"
	"net/url"
	"strconv"
	"strings"

	"github.com/misty-org/misty-server/internal/platform/db"
)

const gigabyte = 1_000_000_000

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
	switch strings.TrimSpace(form.Get("devices")) {
	case "":
		limits["devices"] = nil
	case "0":
		limits["devices"] = int64(0)
	default:
		return nil, errors.New("Set up for use must be Yes or Not yet.")
	}
	return limits, nil
}

func setUpForUse(limit *int64) bool { return limit == nil || *limit > 0 }

func storageInput(limit *int64) string {
	if limit == nil {
		return ""
	}
	return strconv.FormatFloat(float64(*limit)/gigabyte, 'f', -1, 64)
}

func gbLabel(n int64) string {
	return strconv.FormatFloat(float64(n)/gigabyte, 'f', 1, 64) + " GB"
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

func atLimit(used int64, limit *int64) bool { return limit != nil && used >= *limit }

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

func closingLabel(a db.OperatorAccount) string {
	if a.DeleteAt == nil {
		return ""
	}
	return "Closed and read-only: it can sign in and export until " + date(*a.DeleteAt) + ", when its data is deleted unless it is resumed first."
}

func periodLabel(a db.OperatorAccount) string {
	if a.Usage.PeriodStart.IsZero() {
		return "Usage this period"
	}
	return "Usage since " + a.Usage.PeriodStart.UTC().Format("Jan 2")
}
