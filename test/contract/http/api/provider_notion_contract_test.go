package api

import (
	"errors"
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/httpapi"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestNotionCollectionRoutesMatchDiscoveredResourceType(t *testing.T) {
	tests := []struct {
		resourceType string
		query        bool
		want         string
	}{
		{"database", false, "https://api.notion.com/v1/databases/db%2Fone"},
		{"database", true, "https://api.notion.com/v1/databases/db%2Fone/query"},
		{"data_source", false, "https://api.notion.com/v1/data_sources/db%2Fone"},
		{"data_source", true, "https://api.notion.com/v1/data_sources/db%2Fone/query"},
	}
	for _, test := range tests {
		got, err := TestingNotionCollectionEndpoint(test.resourceType, "db/one", test.query)
		if err != nil || got != test.want {
			t.Fatalf("Notion %s query=%v endpoint = %q, %v; want %q", test.resourceType, test.query, got, err, test.want)
		}
	}
}

func TestNotionCollectionRouteRejectsUnselectedObjectKinds(t *testing.T) {
	for _, resourceType := range []string{"", "page", "block", "unknown"} {
		if _, err := TestingNotionCollectionEndpoint(resourceType, "id", true); !errors.Is(err, db.ErrSpaceInvalid) {
			t.Fatalf("Notion resource type %q error = %v, want invalid", resourceType, err)
		}
	}
	if _, err := TestingNotionCollectionEndpoint("data_source", "", true); !errors.Is(err, db.ErrSpaceInvalid) {
		t.Fatalf("empty Notion data source ID error = %v, want invalid", err)
	}
}
