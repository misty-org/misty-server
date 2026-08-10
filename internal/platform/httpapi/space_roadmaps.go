package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func expectedRoadmapVersion(r *http.Request) (int64, error) {
	return strconv.ParseInt(r.URL.Query().Get("expected_version"), 10, 64)
}

func (s *SpacesService) SpaceAgenda() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		from, fromErr := time.Parse(time.RFC3339, r.URL.Query().Get("from"))
		to, toErr := time.Parse(time.RFC3339, r.URL.Query().Get("to"))
		if fromErr != nil || toErr != nil || !to.After(from) || to.Sub(from) > 370*24*time.Hour {
			writeSpaceError(w, db.ErrSpaceInvalid)
			return
		}
		result, err := s.database.SpaceAgenda(r.Context(), userID, chi.URLParam(r, "spaceID"), from, to)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func (s *SpacesService) SpaceRoadmaps() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID := chi.URLParam(r, "spaceID")
		switch r.Method {
		case http.MethodGet:
			items, err := s.database.SpaceRoadmaps(r.Context(), userID, spaceID)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"roadmaps": items})
		case http.MethodPost:
			var body struct {
				Name        string `json:"name"`
				Description string `json:"description"`
			}
			if decodeJSON(w, r, &body) != nil {
				return
			}
			item, err := s.database.CreateSpaceRoadmap(r.Context(), userID, spaceID, body.Name, body.Description)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusCreated, item)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

func (s *SpacesService) SpaceRoadmapNodeDefinitions() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID := chi.URLParam(r, "spaceID")
		switch r.Method {
		case http.MethodGet:
			items, err := s.database.SpaceRoadmapNodeDefinitions(r.Context(), userID, spaceID)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"node_definitions": items})
		case http.MethodPost:
			var body db.SpaceRoadmapNodeDefinition
			if decodeJSON(w, r, &body) != nil {
				return
			}
			item, err := s.database.CreateSpaceRoadmapNodeDefinition(r.Context(), userID, spaceID, body)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusCreated, item)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

func (s *SpacesService) SpaceRoadmapNodeDefinition() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID, definitionID := chi.URLParam(r, "spaceID"), chi.URLParam(r, "definitionID")
		switch r.Method {
		case http.MethodPatch:
			var body struct {
				db.SpaceRoadmapNodeDefinition
				ExpectedVersion int64 `json:"expected_version"`
			}
			if decodeJSON(w, r, &body) != nil {
				return
			}
			item, err := s.database.UpdateSpaceRoadmapNodeDefinition(r.Context(), userID, spaceID, definitionID, body.SpaceRoadmapNodeDefinition, body.ExpectedVersion)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, item)
		case http.MethodDelete:
			expected, err := expectedRoadmapVersion(r)
			if err != nil {
				writeSpaceError(w, db.ErrSpaceInvalid)
				return
			}
			if err := s.database.ArchiveSpaceRoadmapNodeDefinition(r.Context(), userID, spaceID, definitionID, expected); err != nil {
				writeSpaceError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

func (s *SpacesService) SpaceRoadmap() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID, roadmapID := chi.URLParam(r, "spaceID"), chi.URLParam(r, "roadmapID")
		switch r.Method {
		case http.MethodGet:
			item, err := s.database.SpaceRoadmap(r.Context(), userID, spaceID, roadmapID)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, item)
		case http.MethodPatch:
			var body struct {
				Name            string `json:"name"`
				Description     string `json:"description"`
				ExpectedVersion int64  `json:"expected_version"`
			}
			if decodeJSON(w, r, &body) != nil {
				return
			}
			item, err := s.database.UpdateSpaceRoadmap(r.Context(), userID, spaceID, roadmapID, body.Name, body.Description, body.ExpectedVersion)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, item)
		case http.MethodDelete:
			expected, err := expectedRoadmapVersion(r)
			if err != nil {
				writeSpaceError(w, db.ErrSpaceInvalid)
				return
			}
			version, err := s.database.ArchiveSpaceRoadmap(r.Context(), userID, spaceID, roadmapID, expected)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, db.SpaceRoadmapMutationResult{GraphVersion: version})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

func (s *SpacesService) SpaceRoadmapMilestones() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			db.SpaceRoadmapMilestone
			ExpectedVersion int64 `json:"expected_version"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		item, version, err := s.database.CreateSpaceRoadmapMilestone(r.Context(), userID, chi.URLParam(r, "spaceID"), chi.URLParam(r, "roadmapID"), body.SpaceRoadmapMilestone, body.ExpectedVersion)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"milestone": item, "graph_version": version})
	}
}

func (s *SpacesService) SpaceRoadmapMilestone() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID, roadmapID, milestoneID := chi.URLParam(r, "spaceID"), chi.URLParam(r, "roadmapID"), chi.URLParam(r, "milestoneID")
		switch r.Method {
		case http.MethodPatch:
			var body struct {
				db.SpaceRoadmapMilestone
				ExpectedVersion int64 `json:"expected_version"`
			}
			if decodeJSON(w, r, &body) != nil {
				return
			}
			item, version, err := s.database.UpdateSpaceRoadmapMilestone(r.Context(), userID, spaceID, roadmapID, milestoneID, body.SpaceRoadmapMilestone, body.ExpectedVersion)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"milestone": item, "graph_version": version})
		case http.MethodDelete:
			expected, err := expectedRoadmapVersion(r)
			if err != nil {
				writeSpaceError(w, db.ErrSpaceInvalid)
				return
			}
			version, err := s.database.ArchiveSpaceRoadmapMilestone(r.Context(), userID, spaceID, roadmapID, milestoneID, expected)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, db.SpaceRoadmapMutationResult{GraphVersion: version})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

func (s *SpacesService) SpaceRoadmapGoals() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			db.SpaceRoadmapGoal
			ExpectedVersion int64 `json:"expected_version"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		item, version, err := s.database.CreateSpaceRoadmapGoal(r.Context(), userID, chi.URLParam(r, "spaceID"), chi.URLParam(r, "roadmapID"), body.SpaceRoadmapGoal, body.ExpectedVersion)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"goal": item, "graph_version": version})
	}
}

func (s *SpacesService) SpaceRoadmapGoal() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID, roadmapID, goalID := chi.URLParam(r, "spaceID"), chi.URLParam(r, "roadmapID"), chi.URLParam(r, "goalID")
		switch r.Method {
		case http.MethodPatch:
			var body struct {
				db.SpaceRoadmapGoal
				CompleteManually *bool `json:"complete_manually"`
				ExpectedVersion  int64 `json:"expected_version"`
			}
			if decodeJSON(w, r, &body) != nil {
				return
			}
			item, version, err := s.database.UpdateSpaceRoadmapGoal(r.Context(), userID, spaceID, roadmapID, goalID, body.SpaceRoadmapGoal, body.CompleteManually, body.ExpectedVersion)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"goal": item, "graph_version": version})
		case http.MethodDelete:
			expected, err := expectedRoadmapVersion(r)
			if err != nil {
				writeSpaceError(w, db.ErrSpaceInvalid)
				return
			}
			version, err := s.database.ArchiveSpaceRoadmapGoal(r.Context(), userID, spaceID, roadmapID, goalID, expected)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, db.SpaceRoadmapMutationResult{GraphVersion: version})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

func (s *SpacesService) SpaceRoadmapGoalTasks() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		if r.Method != http.MethodPut {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			TaskIDs         []string `json:"task_ids"`
			ExpectedVersion int64    `json:"expected_version"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		version, err := s.database.ReplaceSpaceRoadmapGoalTasks(r.Context(), userID, chi.URLParam(r, "spaceID"), chi.URLParam(r, "roadmapID"), chi.URLParam(r, "goalID"), body.TaskIDs, body.ExpectedVersion)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, db.SpaceRoadmapMutationResult{GraphVersion: version})
	}
}

func (s *SpacesService) SpaceRoadmapNodes() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			db.SpaceRoadmapNode
			ExpectedVersion int64 `json:"expected_version"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		item, version, err := s.database.CreateSpaceRoadmapNode(r.Context(), userID, chi.URLParam(r, "spaceID"), chi.URLParam(r, "roadmapID"), body.SpaceRoadmapNode, body.ExpectedVersion)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"node": item, "graph_version": version})
	}
}

func (s *SpacesService) SpaceRoadmapNode() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID, roadmapID, nodeID := chi.URLParam(r, "spaceID"), chi.URLParam(r, "roadmapID"), chi.URLParam(r, "nodeID")
		switch r.Method {
		case http.MethodPatch:
			var body struct {
				db.SpaceRoadmapNode
				ExpectedVersion int64 `json:"expected_version"`
			}
			if decodeJSON(w, r, &body) != nil {
				return
			}
			item, version, err := s.database.UpdateSpaceRoadmapNode(r.Context(), userID, spaceID, roadmapID, nodeID, body.SpaceRoadmapNode, body.ExpectedVersion)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"node": item, "graph_version": version})
		case http.MethodDelete:
			expected, err := expectedRoadmapVersion(r)
			if err != nil {
				writeSpaceError(w, db.ErrSpaceInvalid)
				return
			}
			version, err := s.database.ArchiveSpaceRoadmapNode(r.Context(), userID, spaceID, roadmapID, nodeID, expected)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, db.SpaceRoadmapMutationResult{GraphVersion: version})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

func (s *SpacesService) SpaceRoadmapEdges() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID, roadmapID := chi.URLParam(r, "spaceID"), chi.URLParam(r, "roadmapID")
		if r.Method == http.MethodDelete {
			expected, err := expectedRoadmapVersion(r)
			if err != nil {
				writeSpaceError(w, db.ErrSpaceInvalid)
				return
			}
			version, err := s.database.DeleteSpaceRoadmapEdge(r.Context(), userID, spaceID, roadmapID, chi.URLParam(r, "edgeID"), expected)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, db.SpaceRoadmapMutationResult{GraphVersion: version})
			return
		}
		if r.Method != http.MethodPost && r.Method != http.MethodPatch {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			db.SpaceRoadmapEdge
			ExpectedVersion int64 `json:"expected_version"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		body.ID = chi.URLParam(r, "edgeID")
		item, version, err := s.database.SaveSpaceRoadmapEdge(r.Context(), userID, spaceID, roadmapID, body.SpaceRoadmapEdge, body.ExpectedVersion)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		status := http.StatusOK
		if r.Method == http.MethodPost {
			status = http.StatusCreated
		}
		writeJSON(w, status, map[string]any{"edge": item, "graph_version": version})
	}
}

func (s *SpacesService) SpaceRoadmapLayout() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		if r.Method != http.MethodPatch {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			db.SpaceRoadmapLayout
			ExpectedVersion int64 `json:"expected_version"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		version, err := s.database.UpdateSpaceRoadmapLayout(r.Context(), userID, chi.URLParam(r, "spaceID"), chi.URLParam(r, "roadmapID"), body.SpaceRoadmapLayout, body.ExpectedVersion)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, db.SpaceRoadmapMutationResult{GraphVersion: version})
	}
}
