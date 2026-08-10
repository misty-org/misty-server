package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const noteControlResponseLimit = 64 * 1024

var TestingNoteControlHTTPClient = &http.Client{Timeout: 10 * time.Second}

type TestingNoteControlEnvelope struct {
	Command string          `json:"command"`
	Payload json.RawMessage `json:"payload"`
}

// ProcessNoteControlCommands delivers a bounded batch from the transactional
// note outbox. Individual collaboration-service failures remain queued and do
// not stop other notes from progressing.
func (s *SpacesService) ProcessNoteControlCommands(ctx context.Context, limit int) (int, error) {
	commands, err := s.database.PendingNoteControlCommands(ctx, limit)
	if err != nil {
		return 0, err
	}

	delivered := 0
	var processingErrors []error
	for _, command := range commands {
		if err := s.TestingDeliverNoteControlCommand(ctx, command.NoteID, command.Command, command.Payload); err != nil {
			if markErr := s.database.MarkNoteControlFailed(ctx, command.ID, err.Error()); markErr != nil {
				processingErrors = append(processingErrors, fmt.Errorf("record failed note command %s: %w", command.ID, markErr))
			}
			continue
		}
		if err := s.database.MarkNoteControlDelivered(ctx, command.ID); err != nil {
			processingErrors = append(processingErrors, fmt.Errorf("complete note command %s: %w", command.ID, err))
			continue
		}
		delivered++
	}
	return delivered, errors.Join(processingErrors...)
}

func (s *SpacesService) TestingDeliverNoteControlCommand(
	ctx context.Context,
	noteID, command string,
	payload []byte,
) error {
	return s.deliverCollaborationControlCommand(
		ctx,
		"note-room",
		s.TestingJournalCollab.RoomID(noteID),
		command,
		payload,
	)
}

func (s *SpacesService) deliverCollaborationControlCommand(
	ctx context.Context,
	party, room, command string,
	payload []byte,
) error {
	if !json.Valid(payload) {
		return errors.New("collaboration control payload is invalid JSON")
	}
	body, err := json.Marshal(TestingNoteControlEnvelope{Command: command, Payload: json.RawMessage(payload)})
	if err != nil {
		return err
	}
	timestamp := strconv.FormatInt(time.Now().UTC().Unix(), 10)
	endpoint := fmt.Sprintf(
		"https://%s/parties/%s/%s",
		s.TestingJournalCollab.Host,
		party,
		room,
	)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Misty-Timestamp", timestamp)
	request.Header.Set("X-Misty-Signature", s.TestingJournalCollab.SignControlRequest(timestamp, body))

	response, err := TestingNoteControlHTTPClient.Do(request)
	if err != nil {
		return fmt.Errorf("send collaboration control command: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, noteControlResponseLimit))
		return nil
	}
	responseBody, _ := io.ReadAll(io.LimitReader(response.Body, noteControlResponseLimit))
	reason := strings.TrimSpace(string(responseBody))
	if reason == "" {
		reason = http.StatusText(response.StatusCode)
	}
	return fmt.Errorf("collaboration service returned %d: %s", response.StatusCode, reason)
}
