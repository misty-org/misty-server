package console

import (
	"net/http"

	"github.com/a-h/templ"
)

func (s *Server) logsPage(r *http.Request) templ.Component {
	view := s.loadServices(r.Context())
	return logsPage(view, s.composeFile)
}
