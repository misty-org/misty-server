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
	{pattern: `^POST /me/companion/refine-point/\{invocationID\}$`, why: "the desktop companion sharpens its own pointer from a screen crop"},
	{pattern: `^\S+ /me/(deletion|export|avatar|profile|device|telemetry)$`, why: "account identity and security"},
	{pattern: `^POST /me/(email/verify|referral/claim)$`, why: "verifying the account's email and claiming a referral are the person's own account steps"},
	{pattern: `^\S+ /(realtime|agent-voice)/`, why: "live session transports"},
	{pattern: `^POST /me/voice/live/ticket$`, why: "a live transcription transport the person opens by speaking"},
	{pattern: `^POST /ai/me/voice/cleanup$`, why: "tidies the person's own dictation before it is typed"},
	{pattern: `^(POST|PATCH|DELETE) /me/voice/transcripts(/\{transcriptID\})?$`, why: "saves, renames speakers in, or deletes a capture the person started"},
	{pattern: `^POST /me/voice/transcribe$`, tool: "voice.transcribe"},
	{pattern: `^POST /me/memory/events$`, why: "the person's own device records what they read; agents never write Memory"},
	{pattern: `^POST /me/memory/day-summary$`, why: "summarizes a day for the person's Journal; nothing is stored"},
	{pattern: `^POST /me/memory/forget$`, tool: "memory.history.forget"},
	{pattern: `^POST /me/memory/nodes$`, why: "the person writes a memory into the tree by hand; agents keep memories with memory.pref.remember and memory.workflow.save"},
	{pattern: `^POST /me/voice/transcripts/\{transcriptID\}/notes$`, tool: "voice.to_notes"},
	{pattern: `^\S+ /(ai/invocations|ai/complete|misty/agent-execution|misty/agent-followup|agent-runs/)`, why: "starting and steering runs is the runtime itself"},
	{pattern: `^\S+ /(ai/settings|settings/|me/settings$|ai/preferences/|ai/recaps/)`, why: "account settings change in Settings, never by an agent"},
	{pattern: `^POST /integrations/apps/connect$`, tool: "integration.connect"},
	{pattern: `^\S+ /(connections|integrations/apps/connections|provider-callbacks|oauth)/`, why: "connecting an account needs the person to sign in"},
	{pattern: `^POST /integrations/github/repos$`, why: "publishing a project is the person's choice; agents push through their coding tool, which asks first"},
	{pattern: `^\S+ /(me/app-requests|me/agent-interventions)/`, why: "a person's answer to an agent's card"},
	{pattern: `^\S+ /me/coding/`, why: "the person's device reports its coding tools and mirrors a delegated run through these; agents start that run with code.delegate"},
	{pattern: `^(DELETE /me/agent-grants(/\{grantID\})?|POST /me/agent-grants/(site|project))$`,
		why: "remembering or revoking an approval is the person's answer in the island or Settings; agents never widen or narrow their own grants"},
	{pattern: `^\S+ /me/(agent-questions|agent-plans|agent-goals)/|^\S+ /me/conversations/\{conversationID\}/(mode|goal)$`,
		why: "answering questions, approving plans and setting goals are the person's choices; agents ask, propose and report through conversation_ask_user, plan_propose and goal_update"},
	{pattern: `^POST /ai/workflow-schedules/\{scheduleID\}/device-claim$`, why: "the person's computer picks up its own scheduled run before starting it"},
	{pattern: `^\S+ /ai/inbound-channels/\{channel\}$`, why: "letting email or Slack start tasks is the person's choice in Settings"},
	{pattern: `^\S+ /space-invitations/`, why: "accepting an invitation is the invitee's choice"},
	{pattern: `^\S+ /misty/attachments`, why: "chat attachments come from the person's files"},
	{pattern: `^\S+ /misty/conversations`, why: "the person's own chat list"},
	{pattern: `^\S+ /misty/conversation-folders`, why: "folders that arrange the person's own chat list"},
	{pattern: `^\S+ /misty/agents`, tool: "agent.configure"},
	{pattern: `^PUT /ai/agent-methods/\{methodID\}/schedule$`, tool: "memory.workflow.schedule"},
	{pattern: `^DELETE /ai/agent-methods/\{methodID\}/schedule$`, tool: "memory.workflow.unschedule"},
	{pattern: `^\S+ /ai/agent-methods`, tool: "memory.workflow.save"},
	{pattern: `^\S+ /ai/memories/`, tool: "memory.pref.update"},
	{pattern: `^POST /ai/artifacts/`, why: "a person's review of an agent's output"},

	// Spaces and members.
	{pattern: `^POST /spaces$`, tool: "space.manage"},
	{pattern: `^PATCH /spaces/\{spaceID\}$`, tool: "space.manage"},
	{pattern: `^DELETE /spaces/\{spaceID\}$`, why: "deleting a whole Space stays with its owner"},
	{pattern: `^POST /spaces/\{spaceID\}/transfer$`, why: "transferring ownership stays with the owner"},
	{pattern: `^PUT /spaces/\{spaceID\}/items/\{kind\}/\{itemID\}/visibility$`, why: "making an item private or public is its owner's own choice, from the item's menu"},
	{pattern: `^POST /spaces/\{spaceID\}/leave$`, tool: "space.manage"},
	{pattern: `^(POST|DELETE) /spaces/\{spaceID\}/invitations(/\{inviteID\})?$`, tool: "space.manage"},
	{pattern: `^POST /spaces/\{spaceID\}/invitations/\{inviteID\}/resend$`, tool: "space.manage"},
	{pattern: `^DELETE /spaces/\{spaceID\}/members/\{userID\}$`, tool: "space.manage"},
	{pattern: `^\S+ /spaces/\{spaceID\}/code-projects`, why: "a project's files live on its owner's device; only the person links or unlinks it there"},
	{pattern: `^\S+ /spaces/\{spaceID\}/(read|item-state)$|/conversations/\{conversationID\}/read$`, why: "per-person read and pin state"},

	// Messages and threads.
	{pattern: `^POST /spaces/\{spaceID\}/messages$`, tool: "message.send"},
	{pattern: `^POST /spaces/\{spaceID\}/conversations/\{conversationID\}/messages$`, tool: "thread.post"},
	{pattern: `/messages/\{messageID\}`, why: "editing, deleting or reacting to messages is a person's own voice"},
	{pattern: `^POST /spaces/\{spaceID\}/conversations$`, tool: "thread.create"},
	{pattern: `^PATCH /spaces/\{spaceID\}/conversations/\{conversationID\}$`, tool: "item.rename"},
	{pattern: `^DELETE /spaces/\{spaceID\}/conversations/\{conversationID\}$`, tool: "item.delete"},

	// Notes, drawings and tasks.
	{pattern: `^POST /spaces/\{spaceID\}/notes$`, tool: "note.create"},
	{pattern: `^PATCH /spaces/\{spaceID\}/notes/\{noteID\}$`, tool: "note.update"},
	{pattern: `^DELETE /spaces/\{spaceID\}/notes/\{noteID\}$`, tool: "item.delete"},
	{pattern: `^PATCH /spaces/\{spaceID\}/notes/\{noteID\}/metadata$`, tool: "note.tag"},
	{pattern: `^POST /spaces/\{spaceID\}/drawings$`, tool: "drawing.create"},
	{pattern: `^PATCH /spaces/\{spaceID\}/drawings/\{drawingID\}$`, tool: "item.rename"},
	{pattern: `^DELETE /spaces/\{spaceID\}/drawings/\{drawingID\}$`, tool: "item.delete"},
	{pattern: `^\S+ /spaces/\{spaceID\}/(notes|drawings)/\{\w+\}/(assets|collaboration-ticket)`, why: "editor uploads and live collaboration sessions"},

	// Calendar.

	// Library.
	{pattern: `^PATCH /spaces/\{spaceID\}/library/items/\{itemID\}$`, tool: "file.update"},
	{pattern: `^POST /spaces/\{spaceID\}/attachments/\{attachmentID\}/promote$`, tool: "file.promote_attachment"},
	{pattern: `^POST /spaces/\{spaceID\}/library/items/\{itemID\}/trash$`, tool: "item.delete"},
	{pattern: `^POST /spaces/\{spaceID\}/library/items/\{itemID\}/restore$`, why: "restoring from Recently Deleted is the person's own undo"},
	{pattern: `^\S+ /spaces/\{spaceID\}/library/(uploads|imports|items/\{itemID\}/provider-import)`, why: "files come from the person's device or accounts"},
	{pattern: `^\S+ /spaces/\{spaceID\}/library/`, why: "photo edits, stacks, duplicate merges, bulk selections and Memories curation are visual decisions in the Library editor"},

	// Roadmaps.

	// Space files.
	{pattern: `^DELETE /spaces/\{spaceID\}/nodes/\{nodeID\}$`, tool: "item.delete"},
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
