package api

// WorkerEnabled reports startup configuration, not an account preference. A
// disabled processor must not inspect or claim jobs it cannot execute.
func (s *SpaceLibraryService) WorkerEnabled(kind string) bool {
	switch kind {
	case "ai":
		return s.aiEnabled && s.intelligence != nil
	case "edit":
		return s.editingEnabled && s.mediaProcessor != nil
	case "faces":
		return s.peopleEnabled && s.peopleProcessor != nil
	}
	return false
}
