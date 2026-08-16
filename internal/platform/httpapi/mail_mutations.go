package api

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	mailintegration "github.com/kannachi323/misty/server/internal/integrations/mail"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

const maxMailJSONBodyBytes = 28 << 20

type mailThreadActionInput struct {
	ConnectionID string `json:"connection_id"`
	Read         *bool  `json:"read"`
	Archived     *bool  `json:"archived"`
	Starred      *bool  `json:"starred"`
}

type mailDraftAttachmentInput struct {
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	Data        []byte `json:"data"`
	Inline      bool   `json:"inline"`
	ContentID   string `json:"content_id"`
}

type mailDraftInput struct {
	ConnectionID string                     `json:"connection_id"`
	ThreadID     string                     `json:"thread_id"`
	To           []mailAddressDTO           `json:"to"`
	Cc           []mailAddressDTO           `json:"cc"`
	Bcc          []mailAddressDTO           `json:"bcc"`
	ReplyTo      []mailAddressDTO           `json:"reply_to"`
	Subject      string                     `json:"subject"`
	Text         string                     `json:"text"`
	Attachments  []mailDraftAttachmentInput `json:"attachments"`
}

type mailSendDraftInput struct {
	ConnectionID    string `json:"connection_id"`
	AuthoringSource string `json:"authoring_source"`
	Confirmed       bool   `json:"confirmed"`
}

func decodeMailJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxMailJSONBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return rejectInvalidJSON(w)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return rejectInvalidJSON(w)
	}
	return nil
}

func normalizedDraftInput(input mailDraftInput) mailintegration.DraftInput {
	addresses := func(values []mailAddressDTO) []mailintegration.Address {
		result := make([]mailintegration.Address, len(values))
		for index, value := range values {
			result[index] = mailintegration.Address{Name: strings.TrimSpace(value.Name), Email: strings.TrimSpace(value.Email)}
		}
		return result
	}
	attachments := make([]mailintegration.DraftAttachment, len(input.Attachments))
	for index, value := range input.Attachments {
		attachments[index] = mailintegration.DraftAttachment{Filename: strings.TrimSpace(value.Filename),
			ContentType: strings.TrimSpace(value.ContentType), Data: value.Data, Inline: value.Inline,
			ContentID: strings.TrimSpace(value.ContentID)}
	}
	return mailintegration.DraftInput{ThreadID: strings.TrimSpace(input.ThreadID), To: addresses(input.To),
		Cc: addresses(input.Cc), Bcc: addresses(input.Bcc), ReplyTo: addresses(input.ReplyTo),
		Subject: strings.TrimSpace(input.Subject), Text: input.Text, Attachments: attachments}
}

func (s *SpacesService) recordMailAudit(r *http.Request, item db.MailActionAudit) {
	if err := s.database.RecordMailActionAudit(r.Context(), item); err != nil {
		log.Printf("record mail action audit: %v", err)
	}
}

func (s *SpacesService) MailThreadActions() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		var input mailThreadActionInput
		if decodeMailJSON(w, r, &input) != nil {
			return
		}
		threadID := strings.TrimSpace(chi.URLParam(r, "threadID"))
		if threadID == "" || len(threadID) > 320 || (input.Read == nil && input.Archived == nil && input.Starred == nil) {
			writeMailError(w, db.ErrSpaceInvalid)
			return
		}
		provider, account, err := s.mailProvider(r.Context(), userID, input.ConnectionID)
		if err != nil {
			writeMailError(w, err)
			return
		}
		result, err := provider.ModifyThread(r.Context(), threadID, mailintegration.ThreadChanges{
			Read: input.Read, Archived: input.Archived, Starred: input.Starred})
		s.recordMailAudit(r, db.MailActionAudit{UserID: userID, ConnectionID: account.ID, Action: "thread_modify",
			TargetType: "thread", TargetID: threadID, Source: "user", Confirmed: true,
			Success: err == nil, ErrorCode: auditErrorCode(err)})
		if err != nil {
			writeMailError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"thread_id": result.ThreadID,
			"added_labels": result.AddedLabels, "removed_labels": result.RemovedLabels})
	}
}

func (s *SpacesService) MailDrafts() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		var input mailDraftInput
		if decodeMailJSON(w, r, &input) != nil {
			return
		}
		provider, account, err := s.mailProvider(r.Context(), userID, input.ConnectionID)
		if err != nil {
			writeMailError(w, err)
			return
		}
		draft, err := provider.CreateDraft(r.Context(), normalizedDraftInput(input))
		targetID := firstNonempty(draft.ProviderID, "new")
		s.recordMailAudit(r, db.MailActionAudit{UserID: userID, ConnectionID: account.ID, Action: "draft_create",
			TargetType: "draft", TargetID: targetID, Source: "user", Confirmed: true,
			Success: err == nil, ErrorCode: auditErrorCode(err)})
		if err != nil {
			writeMailError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"draft": mailDraftToDTO(draft)})
	}
}

func (s *SpacesService) MailDraft() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		var input mailDraftInput
		if decodeMailJSON(w, r, &input) != nil {
			return
		}
		draftID := strings.TrimSpace(chi.URLParam(r, "draftID"))
		if draftID == "" || len(draftID) > 320 {
			writeMailError(w, db.ErrSpaceInvalid)
			return
		}
		provider, account, err := s.mailProvider(r.Context(), userID, input.ConnectionID)
		if err != nil {
			writeMailError(w, err)
			return
		}
		draft, err := provider.UpdateDraft(r.Context(), draftID, normalizedDraftInput(input))
		s.recordMailAudit(r, db.MailActionAudit{UserID: userID, ConnectionID: account.ID, Action: "draft_update",
			TargetType: "draft", TargetID: draftID, Source: "user", Confirmed: true,
			Success: err == nil, ErrorCode: auditErrorCode(err)})
		if err != nil {
			writeMailError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"draft": mailDraftToDTO(draft)})
	}
}

func (s *SpacesService) MailSendDraft() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		var input mailSendDraftInput
		if decodeMailJSON(w, r, &input) != nil {
			return
		}
		draftID := strings.TrimSpace(chi.URLParam(r, "draftID"))
		source := strings.ToLower(strings.TrimSpace(input.AuthoringSource))
		if draftID == "" || len(draftID) > 320 || (source != "user" && source != "ai") {
			writeMailError(w, db.ErrSpaceInvalid)
			return
		}
		provider, account, err := s.mailProvider(r.Context(), userID, input.ConnectionID)
		if err != nil {
			writeMailError(w, err)
			return
		}
		confirmed := source == "user" || input.Confirmed
		if source == "ai" && !input.Confirmed {
			s.recordMailAudit(r, db.MailActionAudit{UserID: userID, ConnectionID: account.ID, Action: "draft_send",
				TargetType: "draft", TargetID: draftID, Source: source, Confirmed: false,
				Success: false, ErrorCode: "mail_confirmation_required"})
			writeMailError(w, errMailConfirmationNeeded)
			return
		}
		message, err := provider.SendDraft(r.Context(), draftID)
		s.recordMailAudit(r, db.MailActionAudit{UserID: userID, ConnectionID: account.ID, Action: "draft_send",
			TargetType: "draft", TargetID: draftID, Source: source, Confirmed: confirmed,
			Success: err == nil, ErrorCode: auditErrorCode(err)})
		if err != nil {
			writeMailError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"message": mailMessageToDTO(message)})
	}
}

func auditErrorCode(err error) string {
	if err == nil {
		return ""
	}
	return mailErrorCode(err)
}
