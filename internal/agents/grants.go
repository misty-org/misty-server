package agent

import (
	"strings"
)

// Permission modes a chat runs in. Ask pauses before every action that changes
// something; Auto continues when a remembered approval covers the action.
const (
	PermissionAsk  = "ask"
	PermissionAuto = "auto"
)

// NormalizePermissionMode maps stored run modes onto Ask and Auto. The retired
// "full" mode meant Auto; anything unknown is "" so callers fall back to the
// agent's default.
func NormalizePermissionMode(mode string) string {
	switch strings.TrimSpace(mode) {
	case PermissionAsk:
		return PermissionAsk
	case PermissionAuto, ModeFull:
		return PermissionAuto
	}
	return ""
}

// GrantAction is the closed set of actions a person can remember an approval for.
type GrantAction string

const (
	GrantAppAction      GrantAction = "app.action"
	GrantBrowserSend    GrantAction = "browser.send"
	GrantBrowserDelete  GrantAction = "browser.delete"
	GrantBrowserPublish GrantAction = "browser.publish"
	GrantPayment        GrantAction = "payment"
	GrantSpaceAdmin     GrantAction = "space.admin"
	GrantItemDelete     GrantAction = "item.delete"
	GrantScheduleRemove GrantAction = "schedule.remove"
	GrantProjectEdit    GrantAction = "project.edit"
	GrantProjectInstall GrantAction = "project.install"
	GrantGitPush        GrantAction = "git.push"
	GrantRecallRead     GrantAction = "recall.read"

	// Actions that pause but can never be remembered.
	ActionToolWrite      GrantAction = "tool.write"      // any other change Misty makes in Ask
	ActionBrowserStep    GrantAction = "browser.step"    // an ordinary click or entry on a page
	ActionScreenControl  GrantAction = "screen.control"  // a consequential action on the whole screen
	ActionAccountSetting GrantAction = "account.setting" // account and security settings
	ActionOutsideProject GrantAction = "project.outside" // a path outside the attached project
	ActionRecallForget   GrantAction = "memory.history.forget"   // deleting what Memory kept
)

// RecallScopeID is the scope id of a Memory grant: Memory is per person.
const RecallScopeID = "memory"

// ScopeKind names what a grant is limited to.
type ScopeKind string

const (
	ScopeConnectedAccount ScopeKind = "connected_account"
	ScopeOrigin           ScopeKind = "origin"
	ScopeMerchant         ScopeKind = "merchant"
	ScopeSpace            ScopeKind = "space"
	ScopeProject          ScopeKind = "project"
	ScopeRepo             ScopeKind = "repo"
	ScopeSchedule         ScopeKind = "schedule"
	// ScopeRecall is the person's own Memory; its one id is RecallScopeID.
	ScopeRecall ScopeKind = "recall"
)

// ActionClass is how an action behaves in Ask and Auto.
type ActionClass string

const (
	// ClassRead never pauses.
	ClassRead ActionClass = "read"
	// ClassScopeImplied is covered by what the chat already attached: Ask
	// pauses, Auto continues.
	ClassScopeImplied ActionClass = "scope_implied"
	// ClassPermission needs the person: Ask pauses, Auto continues only on a
	// remembered grant.
	ClassPermission ActionClass = "permission"
	// ClassNeverRemember always pauses and offers no remember option.
	ClassNeverRemember ActionClass = "never_remember"
)

// Decision is what happens to one action.
type Decision string

const (
	DecisionAllow Decision = "allow"
	DecisionPause Decision = "pause"
	DecisionDeny  Decision = "deny"
)

var grantActions = map[GrantAction]struct {
	class  ActionClass
	scopes []ScopeKind
}{
	GrantAppAction:       {ClassPermission, []ScopeKind{ScopeConnectedAccount}},
	GrantBrowserSend:     {ClassPermission, []ScopeKind{ScopeOrigin}},
	GrantBrowserDelete:   {ClassPermission, []ScopeKind{ScopeOrigin}},
	GrantBrowserPublish:  {ClassPermission, []ScopeKind{ScopeOrigin}},
	GrantPayment:         {ClassPermission, []ScopeKind{ScopeMerchant}},
	GrantSpaceAdmin:      {ClassPermission, []ScopeKind{ScopeSpace}},
	GrantItemDelete:      {ClassPermission, []ScopeKind{ScopeSpace}},
	GrantScheduleRemove:  {ClassPermission, []ScopeKind{ScopeSchedule}},
	GrantProjectEdit:     {ClassScopeImplied, []ScopeKind{ScopeProject}},
	GrantProjectInstall:  {ClassPermission, []ScopeKind{ScopeProject}},
	GrantGitPush:         {ClassPermission, []ScopeKind{ScopeRepo}},
	GrantRecallRead:      {ClassPermission, []ScopeKind{ScopeRecall}},
	ActionToolWrite:      {ClassScopeImplied, nil},
	ActionBrowserStep:    {ClassScopeImplied, nil},
	ActionScreenControl:  {ClassNeverRemember, nil},
	ActionAccountSetting: {ClassNeverRemember, nil},
	ActionOutsideProject: {ClassNeverRemember, nil},
	ActionRecallForget:   {ClassNeverRemember, nil},
}

// ClassOf is the action's class. Unknown actions need the person every time.
func ClassOf(action GrantAction) ActionClass {
	if spec, ok := grantActions[action]; ok {
		return spec.class
	}
	return ClassNeverRemember
}

// Rememberable reports whether the action can be stored as a grant at all.
func Rememberable(action GrantAction) bool {
	spec, ok := grantActions[action]
	return ok && len(spec.scopes) > 0 && spec.class != ClassNeverRemember
}

// ValidGrantScope reports whether a grant for action may name this scope:
// the right kind, and one concrete resource, never a wildcard or pattern.
func ValidGrantScope(action GrantAction, kind ScopeKind, id string) bool {
	if !Rememberable(action) || !ConcreteScopeID(id) {
		return false
	}
	for _, allowed := range grantActions[action].scopes {
		if allowed == kind {
			return true
		}
	}
	return false
}

// ConcreteScopeID rejects empty, oversized and wildcard scopes.
func ConcreteScopeID(id string) bool {
	id = strings.TrimSpace(id)
	return id != "" && len(id) <= 300 && !strings.ContainsAny(id, "*?")
}

// Decide is the permission rule for one action in one mode. covered is
// whether a remembered grant (with its constraints) covers this exact action.
// Ask is forceful: it pauses before every non-read action and ignores grants.
func Decide(mode string, class ActionClass, covered bool) Decision {
	switch class {
	case ClassRead:
		return DecisionAllow
	case ClassNeverRemember:
		return DecisionPause
	}
	if NormalizePermissionMode(mode) != PermissionAuto {
		return DecisionPause
	}
	if class == ClassScopeImplied || covered {
		return DecisionAllow
	}
	return DecisionPause
}
