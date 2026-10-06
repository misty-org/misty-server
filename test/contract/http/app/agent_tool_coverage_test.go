package app

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	api "github.com/kannachi323/misty/server/internal/platform/httpapi"
)

// Every route that changes data is either reachable by an agent through a
// named tool, deliberately kept from agents (why), or a known gap. A new route
// fails this test until someone decides which, so the agent catalog cannot
// silently fall behind the app. Rules match "METHOD /path" in order.
type agentRouteRule struct {
	pattern string
	tool    string
	why     string
	gap     string
}

var agentRouteRules = []agentRouteRule{
	// Kept from agents.
	{pattern: `^\S+ /internal/`, why: "runtime control plane"},
	{pattern: `^POST /mcp$`, why: "the tool transport itself"},
	{pattern: `^\S+ /(auth/|login$|logout$|register$)`, why: "sign-in and credentials stay with the person"},
	{pattern: `^\S+ /billing/`, why: "payments stay with the person"},
	{pattern: `^\S+ /sync/`, why: "encrypted vault and device sync; agents never hold vault keys"},
	{pattern: `^\S+ /devices(/|$)`, why: "adding, approving, naming and permitting devices is signed with device and vault keys agents never hold"},
	{pattern: `^POST /me/screen-model/\{jobID\}$`, why: "the screen planner's model pass-through during browser.act"},
	{pattern: `^\S+ /me/(deletion|export|library-lock|avatar|profile|device|telemetry)$`, why: "account identity and security"},
	{pattern: `^\S+ /(realtime|agent-voice)/`, why: "live session transports"},
	{pattern: `^\S+ /(ai/invocations|ai/complete|misty/agent-execution|misty/agent-followup|agent-runs/)`, why: "starting and steering runs is the runtime itself"},
	{pattern: `^\S+ /ai/senses`, why: "model choices are account settings"},
	{pattern: `^\S+ /(ai/settings|settings/|me/settings$|ai/preferences/|ai/recaps/)`, why: "account settings change in Settings, never by an agent"},
	{pattern: `^POST /integrations/apps/connect$`, tool: "apps.connect"},
	{pattern: `^\S+ /(connections|integrations/apps/connections|provider-callbacks|oauth)/`, why: "connecting an account needs the person to sign in"},
	{pattern: `^\S+ /(me/app-requests|me/agent-interventions)/`, why: "a person's answer to an agent's card"},
	{pattern: `^\S+ /me/(agent-questions|agent-plans|agent-goals)/|^\S+ /me/conversations/\{conversationID\}/(mode|goal)$`,
		why: "answering questions, approving plans and setting goals are the person's choices; agents ask, propose and report through conversation_ask_user, plan_propose and goal_update"},
	{pattern: `^POST /agent-requests/\{requestID\}/(approve|decline)$`, why: "the agent's owner decides; agents never approve themselves"},
	{pattern: `^POST /a2a/agents/\{agentID\}/token$`, why: "agent credentials are minted by the person's signed-in app"},
	{pattern: `^POST /a2a/`, tool: "agents.request"},
	{pattern: `^\S+ /space-invitations/`, why: "accepting an invitation is the invitee's choice"},
	{pattern: `^\S+ /me/home/apps$`, why: "personal home layout"},
	{pattern: `^POST /ai/(smart-library|media-search)/search$`, tool: "library.search"},
	{pattern: `^POST /search/global/visual$`, tool: "search.all"},
	{pattern: `^\S+ /ai/(smart-library|media-search)/`, why: "device indexing pipelines run by the desktop app"},
	{pattern: `^\S+ /misty/attachments`, why: "chat attachments come from the person's files"},
	{pattern: `^\S+ /misty/conversations`, why: "the person's own chat list"},
	{pattern: `^\S+ /misty/agents`, tool: "agents.configure"},
	{pattern: `^POST /ai/agent-methods/run$`, tool: "methods.use"},
	{pattern: `^PUT /ai/agent-methods/\{methodID\}/schedule$`, tool: "workflows.schedule"},
	{pattern: `^DELETE /ai/agent-methods/\{methodID\}/schedule$`, tool: "workflows.unschedule"},
	{pattern: `^\S+ /ai/agent-methods`, tool: "methods.save"},
	{pattern: `^\S+ /ai/memories/`, tool: "memory.update"},
	{pattern: `^POST /ai/artifacts/`, why: "a person's review of an agent's output"},
	{pattern: `^\S+ /me/space-templates`, why: "personal Space templates are saved from the template picker in Settings"},

	// Spaces and members.
	{pattern: `^POST /spaces$`, tool: "spaces.manage"},
	{pattern: `^PATCH /spaces/\{spaceID\}$`, tool: "spaces.manage"},
	{pattern: `^DELETE /spaces/\{spaceID\}$`, why: "deleting a whole Space stays with its owner"},
	{pattern: `^POST /spaces/\{spaceID\}/transfer$`, why: "transferring ownership stays with the owner"},
	{pattern: `^POST /spaces/\{spaceID\}/leave$`, tool: "spaces.manage"},
	{pattern: `^(POST|DELETE) /spaces/\{spaceID\}/invitations(/\{inviteID\})?$`, tool: "spaces.manage"},
	{pattern: `^POST /spaces/\{spaceID\}/invitations/\{inviteID\}/resend$`, tool: "spaces.manage"},
	{pattern: `^DELETE /spaces/\{spaceID\}/members/\{userID\}$`, tool: "spaces.manage"},
	{pattern: `^PUT /spaces/\{spaceID\}/members/\{userID\}/permissions$`, why: "member permissions are access control set by the owner"},
	{pattern: `^\S+ /spaces/\{spaceID\}/agent-listings/`, why: "publishing an agent is its owner's consent"},
	{pattern: `^\S+ /spaces/\{spaceID\}/(read|item-state)$|/conversations/\{conversationID\}/read$`, why: "per-person read and pin state"},
	{pattern: `^\S+ /spaces/\{spaceID\}/integrations/`, why: "connecting an account needs the person to sign in"},

	// Messages and threads.
	{pattern: `^POST /spaces/\{spaceID\}/messages$`, tool: "messages.send"},
	{pattern: `^POST /spaces/\{spaceID\}/conversations/\{conversationID\}/messages$`, tool: "threads.post"},
	{pattern: `/messages/\{messageID\}`, why: "editing, deleting or reacting to messages is a person's own voice"},
	{pattern: `^POST /spaces/\{spaceID\}/conversations$`, tool: "threads.create"},
	{pattern: `^PATCH /spaces/\{spaceID\}/conversations/\{conversationID\}$`, tool: "items.rename"},
	{pattern: `^DELETE /spaces/\{spaceID\}/conversations/\{conversationID\}$`, tool: "items.delete"},

	// Notes, drawings and tasks.
	{pattern: `^POST /spaces/\{spaceID\}/notes$`, tool: "notes.create"},
	{pattern: `^PATCH /spaces/\{spaceID\}/notes/\{noteID\}$`, tool: "notes.update"},
	{pattern: `^DELETE /spaces/\{spaceID\}/notes/\{noteID\}$`, tool: "items.delete"},
	{pattern: `^PATCH /spaces/\{spaceID\}/notes/\{noteID\}/metadata$`, tool: "notes.tags"},
	{pattern: `^POST /spaces/\{spaceID\}/drawings$`, tool: "drawings.create"},
	{pattern: `^PATCH /spaces/\{spaceID\}/drawings/\{drawingID\}$`, tool: "items.rename"},
	{pattern: `^DELETE /spaces/\{spaceID\}/drawings/\{drawingID\}$`, tool: "items.delete"},
	{pattern: `^\S+ /spaces/\{spaceID\}/(notes|drawings)/\{\w+\}/(assets|collaboration-ticket)`, why: "editor uploads and live collaboration sessions"},
	{pattern: `^POST /spaces/\{spaceID\}/tasks$`, tool: "tasks.create"},
	{pattern: `^PATCH /spaces/\{spaceID\}/tasks/\{taskID\}$`, tool: "tasks.update"},
	{pattern: `^DELETE /spaces/\{spaceID\}/tasks/\{taskID\}$`, tool: "items.delete"},
	{pattern: `^POST /spaces/\{spaceID\}/tasks/\{taskID\}/move$`, why: "ordering cards on a board is visual arrangement; status changes go through tasks.update"},

	// Calendar.
	{pattern: `^POST /spaces/\{spaceID\}/calendar/events$`, tool: "calendar.create"},
	{pattern: `^PATCH /spaces/\{spaceID\}/calendar/events/\{eventID\}$`, tool: "calendar.update"},
	{pattern: `^DELETE /spaces/\{spaceID\}/calendar/events/\{eventID\}$`, tool: "items.delete"},
	{pattern: `^\S+ /spaces/\{spaceID\}/calendar/(sources|sync)`, why: "connecting calendars needs the person to sign in"},

	// Library.
	{pattern: `^PATCH /spaces/\{spaceID\}/library/items/\{itemID\}$`, tool: "library.update"},
	{pattern: `^POST /spaces/\{spaceID\}/attachments/\{attachmentID\}/promote$`, tool: "library.promote_attachment"},
	{pattern: `^(POST|PATCH) /spaces/\{spaceID\}/library/albums(/\{albumID\})?$`, tool: "library.organize"},
	{pattern: `^(POST|DELETE) /spaces/\{spaceID\}/library/albums/\{albumID\}/items`, tool: "library.organize"},
	{pattern: `^DELETE /spaces/\{spaceID\}/library/albums/\{albumID\}$`, tool: "items.delete"},
	{pattern: `^POST /spaces/\{spaceID\}/library/items/\{itemID\}/trash$`, tool: "items.delete"},
	{pattern: `^POST /spaces/\{spaceID\}/library/items/\{itemID\}/restore$`, tool: "library.organize"},
	{pattern: `^\S+ /spaces/\{spaceID\}/library/(albums/\{albumID\}/(order|organization)|album-folders|pins)`, why: "visual arrangement of the Library; agents change content, not layout"},
	{pattern: `^\S+ /spaces/\{spaceID\}/library/(grants|shared|reauthenticate)`, why: "sharing, access and re-authentication"},
	{pattern: `^\S+ /spaces/\{spaceID\}/library/(uploads|imports|items/\{itemID\}/provider-import)`, why: "files come from the person's device or accounts"},
	{pattern: `^\S+ /spaces/\{spaceID\}/library/people`, why: "face groups are biometric data; only people manage them"},
	{pattern: `^\S+ /spaces/\{spaceID\}/library/`, why: "photo edits, stacks, duplicate merges, bulk selections and Memories curation are visual decisions in the Library editor"},

	// Roadmaps.
	{pattern: `^POST /spaces/\{spaceID\}/roadmaps$`, tool: "roadmaps.create"},
	{pattern: `^PATCH /spaces/\{spaceID\}/roadmaps/\{roadmapID\}$`, tool: "roadmaps.update"},
	{pattern: `^\S+ /spaces/\{spaceID\}/roadmaps/\{roadmapID\}/(milestones|goals)`, tool: "roadmaps.plan"},
	{pattern: `^DELETE /spaces/\{spaceID\}/roadmaps/\{roadmapID\}$`, tool: "items.delete"},
	{pattern: `^PATCH /spaces/\{spaceID\}/roadmaps/\{roadmapID\}/layout$`, why: "visual arrangement of the roadmap canvas"},
	{pattern: `^\S+ /spaces/\{spaceID\}/roadmaps/\{roadmapID\}/(edges|nodes)`, tool: "roadmaps.canvas"},
	{pattern: `^\S+ /spaces/\{spaceID\}/roadmap-node-definitions`, why: "custom node types and their fields are designed in the canvas editor"},

	// Space files.
	{pattern: `^DELETE /spaces/\{spaceID\}/nodes/\{nodeID\}$`, tool: "items.delete"},
	{pattern: `^\S+ /spaces/\{spaceID\}/nodes`, why: "shared files point at members' devices with targets the desktop app encrypts"},
}

func TestEveryChangingRouteHasAnAgentDecision(t *testing.T) {
	raw, err := os.ReadFile("routes.golden")
	if err != nil {
		t.Fatal(err)
	}
	tools := api.TestingAgentCatalogToolNames()
	compiled := make([]*regexp.Regexp, len(agentRouteRules))
	used := make([]bool, len(agentRouteRules))
	for index, rule := range agentRouteRules {
		compiled[index] = regexp.MustCompile(rule.pattern)
		decisions := 0
		for _, value := range []string{rule.tool, rule.why, rule.gap} {
			if value != "" {
				decisions++
			}
		}
		if decisions != 1 {
			t.Errorf("rule %q needs exactly one of tool, why or gap", rule.pattern)
		}
		if rule.tool != "" && !slices.Contains(tools, rule.tool) {
			t.Errorf("rule %q names %s, which no agent run offers", rule.pattern, rule.tool)
		}
	}
	gaps := []string{}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] == "GET" || fields[0] == "HEAD" || fields[0] == "OPTIONS" {
			continue
		}
		route := fields[0] + " " + fields[1]
		if strings.HasPrefix(fields[1], "/api/") || strings.HasPrefix(fields[1], "/v1/") {
			continue // Mounted aliases of the same handlers.
		}
		matched := false
		for index, pattern := range compiled {
			if pattern.MatchString(route) {
				used[index], matched = true, true
				if agentRouteRules[index].gap != "" {
					gaps = append(gaps, route)
				}
				break
			}
		}
		if !matched {
			t.Errorf("%s changes data but has no agent decision: map it to a tool, or record why or a gap", route)
		}
	}
	for index, rule := range agentRouteRules {
		if !used[index] {
			t.Errorf("rule %q matches no route; remove it", rule.pattern)
		}
	}
	t.Logf("%d changing routes are known agent gaps", len(gaps))
}
