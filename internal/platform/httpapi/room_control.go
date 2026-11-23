package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
)

func (s *SpacesService) ProcessCollabRoomControlCommands(ctx context.Context, limit int) (int, error) {
	commands, err := s.database.PendingCollabRoomControlCommands(ctx, limit)
	if err != nil {
		return 0, err
	}
	delivered := 0
	var processingErrors []error
	for _, command := range commands {
		err := s.deliverCollaborationControlCommand(ctx, command.Party, command.Room, command.ResourceID,
			command.Command, command.Payload)
		if err != nil {
			mark := s.database.MarkCollabRoomControlFailed
			var refused *collaborationControlRefusal
			if errors.As(err, &refused) && (refused.status == http.StatusBadRequest ||
				refused.status == http.StatusConflict || refused.status == http.StatusGone) {
				mark = s.database.AbandonCollabRoomControlCommand
			}
			if markErr := mark(ctx, command.ID, err.Error()); markErr != nil {
				processingErrors = append(processingErrors, fmt.Errorf("record failed room command %s: %w", command.ID, markErr))
			}
			continue
		}
		if err := s.database.MarkCollabRoomControlDelivered(ctx, command.ID); err != nil {
			processingErrors = append(processingErrors, fmt.Errorf("complete room command %s: %w", command.ID, err))
			continue
		}
		delivered++
	}
	return delivered, errors.Join(processingErrors...)
}
