package db

import (
	"encoding/json"
	. "github.com/kannachi323/misty/server/internal/platform/postgres"
	"os"
	"reflect"
	"testing"
)

func TestNativeSpaceTemplateCompatibility(t *testing.T) {
	raw, err := os.ReadFile("../../../test/fixtures/compatibility/space-templates.json")
	if err != nil {
		t.Fatal(err)
	}
	var templates []SpaceTemplate
	if err := json.Unmarshal(raw, &templates); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(BuiltInSpaceTemplates(), templates) {
		t.Fatal("native built-in Space template catalog differs")
	}
}
