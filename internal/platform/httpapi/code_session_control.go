package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
)

// ProcessCodeSessionControlCommands delivers a bounded batch from the code session outbox to
// the sessions' code rooms. Failed commands stay queued with backoff. A command the room
// refuses for good (an ACL change reaching a room already purged) is settled, not retried.
func (s *SpacesService) ProcessCodeSessionControlCommands(ctx context.Context, limit int) (int, error) {
	commands, err := s.database.PendingCodeSessionControlCommands(ctx, limit)
	if err != nil {
		return 0, err
	}
	delivered := 0
	var processingErrors []error
	for _, command := range commands {
		err := s.deliverCollaborationControlCommand(
			ctx,
			"code-room",
			s.JournalCollab.CodeRoomID(command.SessionID),
			command.SessionID,
			command.Command,
			command.Payload,
		)
		if err != nil {
			mark := s.database.MarkCodeSessionControlFailed
			var refused *collaborationControlRefusal
			if errors.As(err, &refused) &&
				(refused.status == http.StatusBadRequest || refused.status == http.StatusGone) {
				mark = s.database.AbandonCodeSessionControlCommand
			}
			if markErr := mark(ctx, command.ID, err.Error()); markErr != nil {
				processingErrors = append(processingErrors,
					fmt.Errorf("record failed code session command %s: %w", command.ID, markErr))
			}
			continue
		}
		if err := s.database.MarkCodeSessionControlDelivered(ctx, command.ID); err != nil {
			processingErrors = append(processingErrors,
				fmt.Errorf("complete code session command %s: %w", command.ID, err))
			continue
		}
		delivered++
	}
	return delivered, errors.Join(processingErrors...)
}
