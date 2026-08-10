package api

import (
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/httpapi"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestLicenseAllowsUse(t *testing.T) {
	tests := []struct {
		name    string
		license *db.License
		want    bool
	}{
		{name: "active", license: &db.License{Status: db.LicenseStatusActive}, want: true},
		{name: "trialing", license: &db.License{Status: db.LicenseStatusTrialing}, want: true},
		{name: "missing", license: nil, want: false},
		{name: "other", license: &db.License{Status: "cancelled"}, want: false},
	}

	for _, tt := range tests {
		if got := TestingLicenseAllowsUse(tt.license); got != tt.want {
			t.Fatalf("%s: licenseAllowsUse() = %v, want %v", tt.name, got, tt.want)
		}
	}
}
