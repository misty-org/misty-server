package app

import (
	api "github.com/misty-org/misty-server/internal/platform/httpapi"
)

func (s *Server) mountAccountRoutes(prefix string, spaces *api.SpacesService) {
	s.Router.Post(prefix+"/me/voice/transcripts", spaces.VoiceTranscripts())
	s.Router.Get(prefix+"/me/voice/transcripts/{transcriptID}", spaces.VoiceTranscript())
	s.Router.Patch(prefix+"/me/voice/transcripts/{transcriptID}", spaces.VoiceTranscript())
	s.Router.Delete(prefix+"/me/voice/transcripts/{transcriptID}", spaces.VoiceTranscript())
	s.Router.Post(prefix+"/me/voice/transcripts/{transcriptID}/notes", spaces.VoiceTranscriptNotes())
	s.Router.Post(prefix+"/me/voice/transcribe", spaces.VoiceTranscribeLibraryItem())
}
