package api

import db "github.com/kannachi323/misty/server/internal/platform/postgres"

func TestingLicenseAllowsUse(license *db.License) bool {
	if license == nil {
		return false
	}

	return license.Status == db.LicenseStatusActive || license.Status == db.LicenseStatusTrialing
}
