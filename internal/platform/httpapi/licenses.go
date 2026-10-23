package api

import "github.com/kannachi323/misty/server/internal/platform/db"

func TestingLicenseAllowsUse(license *db.License) bool {
	if license == nil {
		return false
	}

	return license.Status == db.LicenseStatusActive || license.Status == db.LicenseStatusTrialing
}
