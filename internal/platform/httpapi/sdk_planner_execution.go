package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"time"

	"github.com/kannachi323/misty/server/internal/browseractions"
	cap "github.com/kannachi323/misty/server/internal/capabilities"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func (s *SpacesService) dispatchSDKPlanner(ctx context.Context, e cap.Execution, bound *db.SDKBoundCapability) (json.RawMessage, error) {
	if bound.Target.ValidatePlanner(bound.Provider) != nil || e.Capability != "tasks.create" {
		return nil, errors.Join(db.ErrAgentToolboxNotAttempted, cap.ErrInvalid)
	}
	var input browseractions.Task
	if json.Unmarshal(e.Input, &input) != nil || input.Destination.TargetID != bound.Target.ID || input.Destination.ContainerReference != cap.PlannerContainer(bound.Target.SpaceID) || input.Destination.Label != bound.Target.Label {
		return nil, errors.Join(db.ErrAgentToolboxNotAttempted, cap.ErrInvalid)
	}
	refs, _ := json.Marshal([]any{map[string]any{"url": input.Source.Reference, "title": input.Source.Label}})
	proposed := db.SpaceTask{ID: "task_" + e.EffectID, SpaceID: bound.Target.SpaceID, Title: input.Title, Notes: input.Text, SourceRefs: refs, DueTimezone: "UTC"}
	if input.DueDate != "" {
		date, err := time.Parse("2006-01-02", input.DueDate)
		if err != nil {
			return nil, errors.Join(db.ErrAgentToolboxNotAttempted, err)
		}
		proposed.DueAt = &date
	}
	task, err := s.database.SpaceTaskForMember(ctx, bound.OwnerUserID, bound.Target.SpaceID, proposed.ID)
	if errors.Is(err, db.ErrSpaceNotFound) {
		task, err = s.database.CreateSpaceTask(ctx, bound.OwnerUserID, proposed)
	}
	if err != nil {
		return nil, err
	}
	// Read back the stored task before reporting a result, including on replay.
	task, err = s.database.SpaceTaskForMember(ctx, bound.OwnerUserID, bound.Target.SpaceID, proposed.ID)
	if err != nil {
		return nil, err
	}
	due := ""
	if task.DueAt != nil {
		due = task.DueAt.UTC().Format("2006-01-02")
	}
	if task.Title != input.Title || task.Notes != input.Text || task.SpaceID != bound.Target.SpaceID || !cap.EqualJSON(task.SourceRefs, refs) || due != input.DueDate {
		return nil, db.ErrAgentToolboxActionUnknown
	}
	link := cap.PlannerContainer(bound.Target.SpaceID) + "?task=" + url.QueryEscape(task.ID)
	evidence := []any{map[string]any{"targetId": e.TargetID, "observedAt": time.Now().UTC().Format(time.RFC3339Nano), "kind": "resource", "reference": link, "revision": strconv.FormatInt(task.Version, 10)}}
	result := map[string]any{"taskReference": link, "destination": input.Destination, "title": task.Title, "text": task.Notes, "source": input.Source, "evidence": evidence}
	if due != "" {
		result["dueDate"] = due
	}
	raw, _ := json.Marshal(map[string]any{"status": "success", "result": result, "partial": false, "evidence": evidence})
	if _, err := cap.ParseBackendOutcome(raw, e.Invocation, e.EffectID, bound.Definition, time.Now()); err != nil {
		return nil, err
	}
	return raw, nil
}
