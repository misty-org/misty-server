package api

// WorkerEnabled reports startup configuration, not an account preference. A
// disabled processor must not inspect or claim jobs it cannot execute.
func (s *SpaceLibraryService) WorkerEnabled(kind string) bool {
	return kind == "edit" && s.editingEnabled && s.mediaProcessor != nil
}
