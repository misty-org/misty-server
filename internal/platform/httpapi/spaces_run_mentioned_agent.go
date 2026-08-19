package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"

	"github.com/go-chi/chi/v5"
)

func (s *SpacesService) runMentionedAgent(ctx context.Context, billingUserID, spaceID, conversationID, agentID, sourceMessageID, triggerKind string, content []db.MessageSpan, fileNodeIDs, attachmentIDs, libraryItemIDs []string) (*db.SpaceMessage, string, error) {
	return s.runMentionedAgentAtDepth(ctx, billingUserID, spaceID, conversationID, agentID, sourceMessageID, triggerKind, content, fileNodeIDs, attachmentIDs, libraryItemIDs, 0)
}

func (s *SpacesService) runMentionedAgentAtDepth(ctx context.Context, billingUserID, spaceID, conversationID, agentID, sourceMessageID, triggerKind string, content []db.MessageSpan, fileNodeIDs, attachmentIDs, libraryItemIDs []string, delegationDepth int) (*db.SpaceMessage, string, error) {
	if _, err := s.database.PersonalAgentForSpace(ctx, billingUserID, spaceID, agentID); err != nil {
		return nil, "", err
	}
	instruction := strings.TrimSpace(renderMessageText(content))
	if instruction == "" {
		return nil, "", db.ErrSpaceInvalid
	}
	run, err := s.database.CreateCreatorAgentRun(ctx, billingUserID, spaceID, agentID, db.CreatorAgentRunInput{Instruction: instruction, ConversationTarget: conversationID})
	if err != nil {
		return nil, "", err
	}
	return nil, run.ID, nil
}

// agentClarificationContext returns only the immediately preceding Agent/user
// exchange. Older chat is deliberately ignored so a completed write request
// cannot grant a later, unrelated message write capabilities.
func (s *SpacesService) agentClarificationContext(ctx context.Context, userID, spaceID, conversationID, sourceMessageID string) (string, string) {
	if conversationID == "" {
		return "", ""
	}
	messages, err := s.database.SpaceConversationMessages(ctx, userID, spaceID, conversationID, 0, 8)
	if err != nil {
		return "", ""
	}
	current := 0
	if sourceMessageID != "" {
		current = -1
		for index, message := range messages {
			if message.ID == sourceMessageID {
				current = index
				break
			}
		}
		if current < 0 {
			return "", ""
		}
	}
	if current+2 >= len(messages) || messages[current+1].SenderKind != "agent" || messages[current+2].SenderKind != "person" {
		return "", ""
	}
	return renderMessageText(messages[current+2].Content), renderMessageText(messages[current+1].Content)
}

func (s *SpacesService) createConversationAgentMessage(ctx context.Context, billingUserID, spaceID, conversationID, agentID, text string) (*db.SpaceMessage, error) {
	if conversationID == "" {
		return s.database.CreateSpaceAgentMessage(ctx, billingUserID, spaceID, agentID, text)
	}
	return s.database.CreateSpaceConversationAgentMessage(ctx, billingUserID, spaceID, conversationID, agentID, text)
}

func (s *SpacesService) prepareSpaceAgentFiles(ctx context.Context, userID, spaceID string, nodeIDs []string) (string, error) {
	if len(nodeIDs) == 0 {
		return "", nil
	}
	if len(nodeIDs) > db.MaxMessageFiles {
		return "", db.ErrSpaceInvalid
	}
	var builder strings.Builder
	for _, nodeID := range uniqueStrings(nodeIDs) {
		node, err := s.database.SpaceNodeSecret(ctx, userID, spaceID, nodeID)
		if err != nil {
			return "", err
		}
		target, err := s.TestingDecryptTarget(node.TargetCipher, node.TargetNonce)
		if err != nil {
			return "", err
		}
		text, err := fetchTemporaryGoogleText(ctx, target)
		if err != nil {
			return "", fmt.Errorf("could not prepare %s: %w", node.DisplayName, err)
		}
		builder.WriteString("\n\n[Attached Space file: ")
		builder.WriteString(node.DisplayName)
		builder.WriteString("]\n")
		builder.WriteString(text)
	}
	return builder.String(), nil
}

func fetchTemporaryGoogleText(ctx context.Context, target string) (string, error) {
	download, err := googleTextDownloadURL(target)
	if err != nil {
		return "", err
	}
	client := &http.Client{
		Timeout: 45 * time.Second,
		CheckRedirect: func(next *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many Google Drive redirects")
			}
			if _, err := TestingValidGoogleDriveTarget(next.URL.String()); err != nil {
				return errors.New("Google Drive redirected to an untrusted host")
			}
			next.Header.Del("Authorization")
			next.Header.Del("Cookie")
			return nil
		},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, download, nil)
	if err != nil {
		return "", err
	}
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", errors.New("Google Drive file is unavailable or no longer shared")
	}
	contentType := strings.ToLower(response.Header.Get("Content-Type"))
	if strings.Contains(contentType, "text/html") || strings.Contains(contentType, "application/pdf") || strings.HasPrefix(contentType, "image/") || strings.HasPrefix(contentType, "video/") || strings.HasPrefix(contentType, "audio/") {
		return "", errors.New("this file needs document extraction that is not available for this run")
	}
	const maxBytes = 50 << 20
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxBytes+1))
	if err != nil {
		return "", err
	}
	if len(raw) > maxBytes {
		return "", errors.New("file exceeds the 50 MiB Agent limit")
	}
	return string(raw), nil // The byte slice is never persisted and becomes unreachable after this run.
}

func googleTextDownloadURL(target string) (string, error) {
	parsed, err := TestingValidGoogleDriveTarget(target)
	if err != nil {
		return "", err
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if parsed.Hostname() == "docs.google.com" && len(parts) >= 3 {
		id := parts[2]
		switch parts[0] {
		case "document":
			return "https://docs.google.com/document/d/" + url.PathEscape(id) + "/export?format=txt", nil
		case "spreadsheets":
			return "https://docs.google.com/spreadsheets/d/" + url.PathEscape(id) + "/export?format=csv", nil
		}
	}
	for index, part := range parts {
		if part == "d" && index+1 < len(parts) && parts[index+1] != "" {
			return "https://drive.usercontent.google.com/download?export=download&id=" + url.QueryEscape(parts[index+1]), nil
		}
	}
	if id := parsed.Query().Get("id"); id != "" {
		return "https://drive.usercontent.google.com/download?export=download&id=" + url.QueryEscape(id), nil
	}
	return "", db.ErrSpaceInvalid
}

func (s *SpacesService) Message() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID, messageID := chi.URLParam(r, "spaceID"), chi.URLParam(r, "messageID")
		if r.Method == http.MethodDelete {
			if err := s.database.DeleteSpaceMessage(r.Context(), userID, spaceID, messageID); err != nil {
				writeSpaceError(w, err)
				return
			}
			_ = s.database.InvalidateSpaceActionSuggestionsForMessage(r.Context(), spaceID, "", messageID)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		var body struct {
			Content     []db.MessageSpan `json:"content"`
			FileNodeIDs []string         `json:"file_node_ids"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		message, err := s.database.UpdateSpaceMessage(r.Context(), userID, spaceID, messageID, body.Content, body.FileNodeIDs)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		_ = s.database.InvalidateSpaceActionSuggestionsForMessage(r.Context(), spaceID, "", messageID)
		writeJSON(w, http.StatusOK, message)
	}
}
