package api

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func slackTimestampAfter(candidate, reference string) bool {
	if reference == "" {
		return candidate != ""
	}
	left, right := strings.SplitN(candidate, ".", 2), strings.SplitN(reference, ".", 2)
	if len(left[0]) != len(right[0]) {
		return len(left[0]) > len(right[0])
	}
	return candidate > reference
}

func sortSlackMessagesOldest(items []SlackChatMessage) {
	sort.SliceStable(items, func(i, j int) bool { return slackTimestampAfter(items[j].Timestamp, items[i].Timestamp) })
}

func (s *SpacesService) syncSlackLink(ctx context.Context, link *db.SpaceSlackLink) (int, error) {
	if link == nil || link.Direction == "outbound" {
		return 0, nil
	}
	token, tokenType, err := s.providerAccessToken(ctx, link.ConnectedByUserID, link.SpaceID, link.IntegrationID)
	if err != nil {
		return 0, err
	}
	provider := s.slackChatProvider(token, tokenType)
	identity, err := provider.Identity(ctx)
	if err != nil {
		return 0, err
	}
	imported, cursor, pageCursor := 0, link.LastMessageTS, ""
	seenCursors := map[string]bool{}
	for {
		page, err := provider.History(ctx, link.ChannelID, link.LastMessageTS, pageCursor)
		if err != nil {
			return imported, err
		}
		sortSlackMessagesOldest(page.Messages)
		for _, message := range page.Messages {
			if slackTimestampAfter(message.Timestamp, cursor) {
				cursor = message.Timestamp
			}
			created, mirrorErr := s.mirrorSlackMessage(ctx, *link, message)
			if mirrorErr != nil {
				return imported, mirrorErr
			}
			if created {
				imported++
			}
			if message.ReplyCount == 0 {
				continue
			}
			replies, replyErr := provider.Replies(ctx, link.ChannelID, message.Timestamp)
			if replyErr != nil {
				return imported, replyErr
			}
			sortSlackMessagesOldest(replies)
			for _, reply := range replies {
				if reply.Timestamp == message.Timestamp {
					continue
				}
				created, mirrorErr := s.mirrorSlackMessage(ctx, *link, reply)
				if mirrorErr != nil {
					return imported, mirrorErr
				}
				if created {
					imported++
				}
			}
		}
		pageCursor = page.NextCursor
		// First connection snapshots the latest 100, matching content backfill.
		// Incremental sync drains every page after the durable timestamp.
		if pageCursor == "" || link.LastMessageTS == "" {
			break
		}
		if seenCursors[pageCursor] {
			return imported, errors.New("slack returned a repeated history cursor")
		}
		seenCursors[pageCursor] = true
	}
	now := time.Now().UTC()
	return imported, s.database.SetSpaceSlackLinkSync(ctx, link.ID, cursor, "active", "", identity.UserID, &now)
}

func (s *SpacesService) mirrorSlackMessage(ctx context.Context, link db.SpaceSlackLink, message SlackChatMessage) (bool, error) {
	if !TestingShouldMirrorSlackMessage(message, &link) {
		return false, nil
	}
	content := TestingSlackContentToSpans(message)
	threadID := message.ThreadTimestamp
	if threadID == "" && message.ReplyCount > 0 {
		threadID = message.Timestamp
	}
	origin := db.MessageOrigin{System: "slack", ExternalID: message.Timestamp,
		ExternalChannelID: link.ChannelID, ExternalThreadID: threadID,
		AuthorName: firstNonempty(message.UserID, "Slack user"), AuthorHandle: message.UserID,
		Deleted: message.Deleted}
	if occurred := slackTimestamp(message.Timestamp); occurred != nil {
		origin.AuthoredAt = occurred.Format(time.RFC3339Nano)
	}
	for _, file := range message.Files {
		if file.URL != "" {
			origin.AttachmentURLs = append(origin.AttachmentURLs, file.URL)
		}
	}
	contentRecord, _ := json.Marshal(map[string]any{"type": message.Type, "subtype": message.Subtype,
		"channel": link.ChannelID, "user": message.UserID, "bot_id": message.BotID,
		"text": message.Text, "ts": message.Timestamp, "thread_ts": threadID,
		"deleted": message.Deleted, "files": message.Files})
	var deletedAt *time.Time
	if message.Deleted {
		now := time.Now().UTC()
		deletedAt = &now
	}
	if err := s.database.UpsertProviderContentRecord(ctx, db.ProviderContentRecord{SpaceID: link.SpaceID,
		SharedResourceID: link.SharedResourceID, Provider: "slack", ExternalRecordID: message.Timestamp,
		ParentExternalID: threadID, RecordType: "message", Fingerprint: providerPayloadFingerprint(contentRecord),
		DisplayName: "#" + strings.TrimPrefix(link.ChannelName, "#") + " · " + message.UserID,
		MIMEType:    "application/vnd.slack.message+json", OccurredAt: slackTimestamp(message.Timestamp),
		Content: contentRecord, DeletedAt: deletedAt}); err != nil {
		return false, err
	}
	_, created, err := s.database.UpsertProviderMirroredMessage(ctx, db.ProviderMirroredMessage{
		SpaceID: link.SpaceID, ConversationID: link.ConversationID,
		ConnectedByUserID: link.ConnectedByUserID, Provider: "slack", Content: content, Origin: origin})
	return created, err
}

func TestingShouldMirrorSlackMessage(message SlackChatMessage, link *db.SpaceSlackLink) bool {
	if link == nil || link.Direction == "outbound" || message.Timestamp == "" || message.Type != "message" {
		return false
	}
	if message.BotID != "" || (link.BotUserID != "" && message.UserID == link.BotUserID) {
		return false
	}
	if !message.Deleted && message.Subtype != "" && message.Subtype != "file_share" && message.Subtype != "thread_broadcast" {
		return false
	}
	return strings.TrimSpace(message.Text) != "" || len(message.Files) > 0
}

func TestingSlackContentToSpans(message SlackChatMessage) []db.MessageSpan {
	text := strings.TrimSpace(message.Text)
	if len(message.Files) > 0 {
		names := make([]string, 0, len(message.Files))
		for _, file := range message.Files {
			names = append(names, firstNonempty(strings.TrimSpace(file.Name), "Slack file"))
		}
		if text != "" {
			text += "\n"
		}
		text += "📎 " + strings.Join(names, ", ")
	}
	runes := []rune(text)
	if len(runes) > db.MaxMessageChars {
		text = string(runes[:db.MaxMessageChars-1]) + "…"
	}
	return []db.MessageSpan{{Type: "text", Text: text}}
}
