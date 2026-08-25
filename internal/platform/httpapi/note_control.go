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
	if !json.Valid(payload) {
		return nil, errors.New("collaboration control payload is invalid JSON")
	}
	body, err := json.Marshal(TestingNoteControlEnvelope{Command: command, Payload: json.RawMessage(payload)})
	if err != nil {
		return nil, err
	}
	timestamp := strconv.FormatInt(time.Now().UTC().Unix(), 10)
	endpoint := fmt.Sprintf(
		"%s/parties/%s/%s",
		s.TestingJournalCollab.httpOrigin(),
		party,
		room,
	)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Misty-Timestamp", timestamp)
	request.Header.Set("X-Misty-Signature", s.TestingJournalCollab.SignControlRequest(timestamp, body))
	// The Hosted worker deliberately sees only opaque room IDs. A self-hosted
	// service persists through the local API, so it also needs the local
	// resource ID to address that storage row.
	if InstanceConfigFromEnv().Deployment == "self_hosted" {
		request.Header.Set("X-Misty-Resource-ID", resourceID)
	}

	response, err := TestingNoteControlHTTPClient.Do(request)
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
	return nil, fmt.Errorf("collaboration service returned %d: %s", response.StatusCode, reason)
}
