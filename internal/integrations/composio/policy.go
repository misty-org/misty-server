package composio

import "strings"

// Kind is what running a tool does to the user's accounts.
type Kind string

const (
	KindRead Kind = "read"
	// KindWrite creates or changes the user's own data.
	KindWrite Kind = "write"
	// KindConsequential reaches other people or money: sending, publishing,
	// sharing, inviting, paying or changing access.
	KindConsequential Kind = "consequential"
	// KindDestructive removes data.
	KindDestructive Kind = "destructive"
)

// NeedsApproval reports whether the account's ask-first setting applies.
func (k Kind) NeedsApproval() bool { return k == KindConsequential || k == KindDestructive }

var (
	destructiveWords   = words("DELETE", "DELETES", "REMOVE", "REMOVES", "TRASH", "PURGE", "ERASE", "DESTROY", "WIPE")
	consequentialWords = words("SEND", "SENDS", "REPLY", "FORWARD", "POST", "POSTS", "PUBLISH", "SHARE", "SHARING", "INVITE",
		"TWEET", "RETWEET", "BROADCAST", "SUBMIT", "TRANSFER", "PAY", "PAYMENT", "PURCHASE", "BUY", "ORDER", "CHARGE",
		"REFUND", "PERMISSION", "PERMISSIONS", "REVOKE", "GRANT", "MERGE", "DEPLOY", "CANCEL")
	readWords = words("GET", "LIST", "FETCH", "SEARCH", "FIND", "READ", "RETRIEVE", "QUERY", "LOOKUP", "VIEW", "COUNT", "CHECK")
)

func words(values ...string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, value := range values {
		set[value] = true
	}
	return set
}

// Classify uses Composio's behavior tags first, then the action the slug
// names. It classifies a tool, never the user's request.
func Classify(slug string, tags []string) Kind {
	tagged := words(tags...)
	parts := strings.Split(strings.ToUpper(slug), "_")
	contains := func(set map[string]bool) bool {
		for _, part := range parts {
			if set[part] {
				return true
			}
		}
		return false
	}
	switch {
	case tagged["destructiveHint"]:
		return KindDestructive
	case tagged["readOnlyHint"]:
		return KindRead
	case contains(destructiveWords):
		return KindDestructive
	case contains(consequentialWords):
		return KindConsequential
	case !tagged["createHint"] && !tagged["updateHint"] && len(parts) > 1 && readWords[parts[1]]:
		// Untagged tools name their action after the toolkit: GMAIL_FETCH_EMAILS.
		return KindRead
	}
	return KindWrite
}
