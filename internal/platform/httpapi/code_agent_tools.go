package api

import (
	"context"
	"encoding/json"
	"strings"

	serveragent "github.com/kannachi323/misty/server/internal/agents"
	"github.com/kannachi323/misty/server/internal/agenttools"
)

// Code tools work in a project: a folder the person shared with write access
// on the computer they're chatting from (docs/plans/agent-code-projects.md).
// Each call is a device job; the desktop checks the run grant, then runs it in
// its sandbox. The server sees the arguments and results, never the folder.

type codeTool struct {
	name, description, risk, audit string
	properties                     map[string]any
	required                       []string
}

func codeToolDefinitions() []codeTool {
	text := func(description string, maxLength int) map[string]any {
		return map[string]any{"type": "string", "maxLength": maxLength, "description": description}
	}
	path := text("Path inside the project, relative to its top folder, such as src/index.html.", 1000)
	folder := text("Folder inside the project. Omit for the top folder.", 1000)
	integer := func(description string, minimum, maximum int) map[string]any {
		return map[string]any{"type": "integer", "minimum": minimum, "maximum": maximum, "description": description}
	}
	processID := text("Process id from code_start or code_processes.", 40)
	return []codeTool{
		{name: "code.read", risk: serveragent.RiskRead,
			description: "Read a text file in the project. Each line comes back as `12:k3f|text`: the line number, a short hash of the line, then its text. The number:hash pair is the line's anchor for code_edit. Long files come in pages; use offset and limit for more.",
			properties:  map[string]any{"path": path, "offset": integer("First line to read (1-based).", 1, 10_000_000), "limit": integer("How many lines (default 400).", 1, 2000)},
			required:    []string{"path"}},
		{name: "code.list", risk: serveragent.RiskRead,
			description: "List one folder of the project: names and whether each is a file or folder.",
			properties:  map[string]any{"path": folder}},
		{name: "code.find", risk: serveragent.RiskRead,
			description: "Find files by glob, such as *.css (any folder) or src/**/*.ts. Skips files Git ignores and node_modules.",
			properties:  map[string]any{"pattern": text("Glob pattern.", 300), "path": folder},
			required:    []string{"pattern"}},
		{name: "code.search", risk: serveragent.RiskRead,
			description: "Search file contents with a regular expression (Rust syntax). Matches come back as `path:line:hash|text`, with anchors for code_edit. Skips files Git ignores and node_modules.",
			properties: map[string]any{
				"pattern": text("Regular expression.", 1000), "path": text("Folder or file to search. Omit for the whole project.", 1000),
				"glob": text("Only files matching this glob, such as *.html.", 300), "ignoreCase": map[string]any{"type": "boolean"},
				"maxResults": integer("Default 100.", 1, 300),
			},
			required: []string{"pattern"}},
		{name: "code.write", risk: serveragent.RiskWrite, audit: "code.file.written",
			description: "Create a file, or replace a whole file, with the complete content. Folders are created as needed. For changes to an existing file, code_edit is cheaper.",
			properties:  map[string]any{"path": path, "content": text("The whole file.", 2_000_000)},
			required:    []string{"path", "content"}},
		{name: "code.edit", risk: serveragent.RiskWrite, audit: "code.file.edited",
			description: "Change part of a file, in one of two ways. Text: oldText copied exactly from the file (unique, with indentation) and newText. Lines: start (and optionally end) as anchors from code_read, like 12:k3f, with content to replace those lines (empty content deletes them); or after (an anchor, or 0 for the top) with content to insert. Anchors must match the current file, so read again after other changes. Returns the edited lines with fresh anchors.",
			properties: map[string]any{
				"path": path, "oldText": text("Exact text to replace.", 200_000), "newText": text("Replacement text.", 200_000),
				"replaceAll": map[string]any{"type": "boolean", "description": "Replace every occurrence of oldText."},
				"start":      text("First line to replace, as number:hash.", 40), "end": text("Last line to replace, as number:hash.", 40),
				"after": text("Insert after this line (number:hash), or 0 for the top.", 40), "content": text("Lines for start/end or after.", 200_000),
			},
			required: []string{"path"}},
		{name: "code.run", risk: serveragent.RiskWrite, audit: "code.command.run",
			description: "Run a bash command in the project and wait for it to finish (timeoutSeconds, default 120, at most 240). Commands run in a sandbox: they can change only this project, can't read the rest of the computer or the project's .env and key files, can't change .git, can't open network ports, and reach the internet only to install packages when the user allowed that for this project. Output is trimmed to its start and end. A non-zero exit code is a normal result to read and act on.",
			properties: map[string]any{
				"command": text("The bash command.", 16_000), "cwd": folder,
				"timeoutSeconds": integer("Default 120.", 1, 240),
			},
			required: []string{"command"}},
		{name: "code.start", risk: serveragent.RiskWrite, audit: "code.process.started",
			description: "Start a long-running command in the background, such as a build watcher (npx vite build --watch). It keeps running after this call; read its output with code_logs and stop it with code_stop. Commands can't listen for connections, so dev servers won't work; build the site and use code_preview.",
			properties:  map[string]any{"command": text("The bash command.", 16_000), "cwd": folder, "name": text("A short label.", 80)},
			required:    []string{"command"}},
		{name: "code.logs", risk: serveragent.RiskRead,
			description: "Read the output of a background process, and whether it is still running.",
			properties:  map[string]any{"id": processID}, required: []string{"id"}},
		{name: "code.processes", risk: serveragent.RiskRead,
			description: "List this project's background processes.",
			properties:  map[string]any{}},
		{name: "code.stop", risk: serveragent.RiskWrite, audit: "code.process.stopped",
			description: "Stop a background process.",
			properties:  map[string]any{"id": processID}, required: []string{"id"}},
		{name: "git.status", risk: serveragent.RiskRead,
			description: "Show the project's Git state: its branch, its GitHub repository if any, commits not yet on GitHub (ahead) or not yet pulled (behind), and the changed files.",
			properties:  map[string]any{}},
		{name: "git.diff", risk: serveragent.RiskRead,
			description: "Show changes as a diff: the uncommitted changes against the last commit, or what one commit changed (commit, a hash or HEAD). Secret files never appear.",
			properties:  map[string]any{"commit": text("A commit to show. Omit for uncommitted changes.", 64)}},
		{name: "git.log", risk: serveragent.RiskRead,
			description: "List recent commits on the current branch, newest first, marking which are on GitHub.",
			properties:  map[string]any{"limit": integer("Default 30.", 1, 200)}},
		{name: "git.commit", risk: serveragent.RiskWrite, audit: "git.commit.created",
			description: "Commit changes with a short message that says what changed and why. Commits every change, or only the given paths. Secret files and files over 50 MB are never committed; the result names any it left out. Commit when a piece of work is done and checked.",
			properties: map[string]any{
				"message": text("The commit message: a short summary line, optionally a blank line and more detail.", 10_000),
				"paths":   map[string]any{"type": "array", "maxItems": 200, "items": text("A file or folder in the project.", 1000)},
			},
			required: []string{"message"}},
		{name: "git.branch", risk: serveragent.RiskWrite, audit: "git.branch.switched",
			description: "Create a branch from the current commit and switch to it (create: true), or switch to an existing branch. Refuses while there are uncommitted changes.",
			properties:  map[string]any{"name": text("Branch name, like add-contact-page.", 120), "create": map[string]any{"type": "boolean"}},
			required:    []string{"name"}},
		{name: "git.push", risk: serveragent.RiskWrite, audit: "git.pushed",
			description: "Send the current branch's commits to the project's GitHub repository. Works only when the person allowed agents to push this project. Push your own branch and then open a pull request; the default branch only when the person allowed that too. Never force-pushes.",
			properties:  map[string]any{}},
		{name: "git.pull", risk: serveragent.RiskWrite, audit: "git.pulled",
			description: "Bring the current branch up to date with GitHub when that's a fast-forward. Never merges; says so when the histories have diverged or uncommitted changes are in the way.",
			properties:  map[string]any{}},
		{name: "git.pull_request", risk: serveragent.RiskWrite, audit: "git.pull_request.opened",
			description: "Open a pull request on GitHub from the current branch (push it first) into the default branch, with a clear title and a short description of what changed and how it was checked. Returns its link; give it to the person.",
			properties:  map[string]any{"title": text("Pull request title.", 250), "body": text("What changed and how it was checked.", 20_000)},
			required:    []string{"title"}},
		{name: "code.preview", risk: serveragent.RiskWrite, audit: "code.preview.opened",
			description: "Show the project in a tab of the user's Misty browser and return its local address. path is a page (such as about.html) served from the project's top folder, or a folder served as the site's root (such as dist after a build). Omit path for index.html at the top. Check the result with the browser tools, then tell the user it's open.",
			properties:  map[string]any{"path": text("Page or folder to preview.", 1000)}},
	}
}

func (s *SpacesService) projectToolRegistrations(projects []deviceGrant) []agenttools.Registration {
	labels := make([]string, 0, len(projects))
	for _, project := range projects {
		labels = append(labels, folderLabel(project))
	}
	resolve := func(value string) (deviceGrant, bool) {
		value = strings.TrimSpace(value)
		if value == "" && len(projects) == 1 {
			return projects[0], true
		}
		matches := []deviceGrant{}
		for _, candidate := range projects {
			if candidate.scopeID == value || strings.EqualFold(folderLabel(candidate), value) {
				return candidate, true
			}
			if strings.EqualFold(candidate.name, value) {
				matches = append(matches, candidate)
			}
		}
		if len(matches) == 1 {
			return matches[0], true
		}
		return deviceGrant{}, false
	}
	registrations := make([]agenttools.Registration, 0, len(codeToolDefinitions()))
	for _, tool := range codeToolDefinitions() {
		properties := map[string]any{}
		for key, value := range tool.properties {
			properties[key] = value
		}
		required := append([]string{}, tool.required...)
		if len(projects) > 1 {
			properties["project"] = map[string]any{"type": "string", "minLength": 1, "maxLength": 300, "description": "Which project: " + strings.Join(labels, ", ")}
			required = append(required, "project")
		}
		descriptor := deviceToolDescriptor(tool.name, tool.description, tool.risk, tool.audit, agentToolSchema(properties, required))
		registrations = append(registrations, agenttools.Registration{Descriptor: descriptor, Handler: func(ctx context.Context, invocation agenttools.Invocation, request serveragent.ToolRequest) (json.RawMessage, error) {
			var input map[string]any
			if json.Unmarshal(request.Arguments, &input) != nil || input == nil {
				input = map[string]any{}
			}
			wanted, _ := input["project"].(string)
			target, ok := resolve(wanted)
			if !ok {
				return nil, serveragent.ErrInvalidRequest("name the project: " + strings.Join(labels, ", "))
			}
			delete(input, "project")
			return s.runDeviceTool(ctx, invocation, request.ID, target.scopeID, request.Name, TestingMustAPIRawJSON(input))
		}})
	}
	return registrations
}

// TestingProjectToolDescriptors exposes the code tool descriptors a chat with
// these project folders (by display name) would see.
func TestingProjectToolDescriptors(names ...string) []agenttools.Descriptor {
	projects := make([]deviceGrant, 0, len(names))
	for index, name := range names {
		projects = append(projects, deviceGrant{scopeID: "scope_" + string(rune('a'+index)), name: name, deviceID: "device-1"})
	}
	var service SpacesService
	descriptors := []agenttools.Descriptor{}
	for _, registration := range service.projectToolRegistrations(projects) {
		descriptors = append(descriptors, registration.Descriptor)
	}
	return descriptors
}
