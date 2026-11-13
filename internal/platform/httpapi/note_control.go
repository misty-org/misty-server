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

const collaborationControlResponseLimit = 6 << 20

var NoteControlHTTPClient = &http.Client{Timeout: 10 * time.Second}

type NoteControlEnvelope struct {
	ResourceID string          `json:"resource_id"`
	Command    string          `json:"command"`
	Payload    json.RawMessage `json:"payload"`
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
		if err := s.DeliverNoteControlCommand(ctx, command.NoteID, command.Command, command.Payload); err != nil {
			mark := s.database.MarkNoteControlFailed
			var refused *collaborationControlRefusal
			if errors.As(err, &refused) && refused.status == http.StatusBadRequest {
				// The room refused the command itself; it would refuse it again.
				mark = s.database.AbandonNoteControlCommand
			}
			if markErr := mark(ctx, command.ID, err.Error()); markErr != nil {
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

func (s *SpacesService) DeliverNoteControlCommand(
	ctx context.Context,
	noteID, command string,
	payload []byte,
) error {
	return s.deliverCollaborationControlCommand(
		ctx,
		"note-room",
		s.JournalCollab.RoomID(noteID),
		noteID,
		command,
		payload,
	)
}

func (s *SpacesService) deliverCollaborationControlCommand(
	ctx context.Context,
	party, room, resourceID, command string,
	payload []byte,
) error {
	_, err := s.requestCollaborationControlCommand(ctx, party, room, resourceID, command, payload)
	return err
}

func (s *SpacesService) requestCollaborationControlCommand(
	ctx context.Context,
	party, room, resourceID, command string,
	payload []byte,
) (json.RawMessage, error) {
	if !s.JournalCollab.Enabled {
		return nil, ErrCollaborationUnconfigured
	}
	if !json.Valid(payload) {
		return nil, errors.New("collaboration control payload is invalid JSON")
	}
	body, err := json.Marshal(NoteControlEnvelope{ResourceID: resourceID, Command: command, Payload: json.RawMessage(payload)})
	if err != nil {
		return nil, err
	}
	timestamp := strconv.FormatInt(time.Now().UTC().Unix(), 10)
	endpoint := fmt.Sprintf(
		"%s/parties/%s/%s",
		s.JournalCollab.httpOrigin(),
		party,
		room,
	)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Misty-Timestamp", timestamp)
	request.Header.Set("X-Misty-Signature", s.JournalCollab.SignControlRequest(timestamp, body))

	response, err := NoteControlHTTPClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("send collaboration control command: %w", err)
	}
	defer response.Body.Close()
	responseBody, readErr := io.ReadAll(io.LimitReader(response.Body, collaborationControlResponseLimit+1))
	if readErr != nil {
		return nil, fmt.Errorf("read collaboration control response: %w", readErr)
	}
	if len(responseBody) > collaborationControlResponseLimit {
		return nil, errors.New("collaboration control response is too large")
	}
	if response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices {
		if len(responseBody) == 0 {
			responseBody = []byte(`{}`)
		}
		if !json.Valid(responseBody) {
			return nil, errors.New("collaboration service returned invalid JSON")
		}
		return json.RawMessage(responseBody), nil
	}
	reason := strings.TrimSpace(string(responseBody))
	if reason == "" {
		reason = http.StatusText(response.StatusCode)
	}
	return nil, &collaborationControlRefusal{status: response.StatusCode, reason: reason}
}

// collaborationControlRefusal is a control command the collaboration service
// answered with an error status.
type collaborationControlRefusal struct {
	status int
	reason string
}

func (err *collaborationControlRefusal) Error() string {
	return fmt.Sprintf("collaboration service returned %d: %s", err.status, err.reason)
}
