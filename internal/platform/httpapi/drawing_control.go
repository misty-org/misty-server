package api

import (
	"context"
	"errors"
	"fmt"
)

// ProcessDrawingControlCommands delivers a bounded batch from the drawing
// outbox. Failed commands stay queued with backoff.
func (s *SpacesService) ProcessDrawingControlCommands(
	ctx context.Context,
	limit int,
) (int, error) {
	commands, err := s.database.PendingDrawingControlCommands(ctx, limit)
	if err != nil {
		return 0, err
	}

	delivered := 0
	var processingErrors []error
	for _, command := range commands {
		err := s.deliverCollaborationControlCommand(
			ctx,
			"drawing-room",
			s.TestingJournalCollab.DrawingRoomID(command.DrawingID),
			command.DrawingID,
			command.Command,
			command.Payload,
		)
		if err != nil {
			if markErr := s.database.MarkDrawingControlFailed(
				ctx, command.ID, err.Error(),
			); markErr != nil {
				processingErrors = append(
					processingErrors,
					fmt.Errorf(
						"record failed drawing command %s: %w",
						command.ID,
						markErr,
					),
				)
			}
			continue
		}
		if err := s.database.MarkDrawingControlDelivered(
			ctx, command.ID,
		); err != nil {
			processingErrors = append(
				processingErrors,
				fmt.Errorf("complete drawing command %s: %w", command.ID, err),
			)
			continue
		}
		delivered++
	}
	return delivered, errors.Join(processingErrors...)
}
