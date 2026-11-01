package console

import (
	"net/http"

	"github.com/a-h/templ"
)

// The Logs page follows any service of the stack in every environment. It
// shares the Services page's compose runner and log panel; only Services
// (development) may start, stop and restart containers.
func (s *Server) logsPage(r *http.Request) templ.Component {
	view := s.loadServices(r.Context())
	return logsPage(view, s.composeFile)
}
