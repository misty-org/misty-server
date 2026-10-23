package api

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/kannachi323/misty/server/internal/agent"
	"github.com/kannachi323/misty/server/internal/agenttools"
)

// Sending a file saves it to the chat's Space Library: any shared folder can
// do it, and it names no destination computer.
func TestFilesSendSavesToTheSpaceLibrary(t *testing.T) {
	service := &SpacesService{}
	registrations := service.folderToolRegistrations([]deviceGrant{{scopeID: "scope_reports", name: "Reports", deviceID: "device-1", deviceName: "Studio Mac"}})
	var send *agenttools.Registration
	for index := range registrations {
		if registrations[index].Descriptor.Name == filesSendTool {
			send = &registrations[index]
		}
	}
	if send == nil {
		t.Fatal("a shared folder did not offer folder.send")
	}
	if !strings.Contains(send.Descriptor.Description, "Space Library") || strings.Contains(strings.ToLower(send.Descriptor.Description), "local network") {
		t.Fatalf("folder.send description = %q", send.Descriptor.Description)
	}
	var schema struct {
		Properties map[string]any `json:"properties"`
		Required   []string       `json:"required"`
	}
	if err := json.Unmarshal(send.Descriptor.InputSchema, &schema); err != nil {
		t.Fatal(err)
	}
	if len(schema.Properties) != 2 || schema.Properties["folder"] == nil || schema.Properties["path"] == nil || strings.Join(schema.Required, ",") != "folder,path" {
		t.Fatalf("folder.send schema = %s", send.Descriptor.InputSchema)
	}
	invocation := agenttools.Invocation{UserID: "user-1", RunID: "invocation_1"}
	var invalid agent.ErrInvalidRequest
	for _, arguments := range []string{`{"folder":"Taxes","path":"a.pdf"}`, `{"folder":"Reports","path":""}`, `{"folder":"Reports","path":"../a.pdf"}`} {
		if _, err := send.Handler(t.Context(), invocation, agent.ToolRequest{ID: "c1", Name: filesSendTool, Arguments: json.RawMessage(arguments)}); !errors.As(err, &invalid) {
			t.Fatalf("%s = %v", arguments, err)
		}
	}
}
