package console

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"

	"github.com/a-h/templ"
	"github.com/go-chi/chi/v5"
)

// composeService is one row of `docker compose ps --format json`.
type composeService struct {
	Service  string `json:"Service"`
	State    string `json:"State"`
	Health   string `json:"Health"`
	Status   string `json:"Status"`
	ExitCode int    `json:"ExitCode"`
}

// status is the health when the container defines a healthcheck, else state.
func (c composeService) status() string {
	if c.Health != "" {
		return c.Health
	}
	return c.State
}

// setupJobs finish and exit; successful ones are hidden like `misty server status`.
var setupJobs = []string{"setup", "dev-init", "migrate", "database-permissions", "agent-runtime-setup", "cloudflare-deploy"}

type composeRunner interface {
	PS(ctx context.Context) ([]composeService, error)
	Logs(ctx context.Context, service string, tail int) (string, error)
	Run(ctx context.Context, action, service string) error
}

// dockerCompose drives the development stack exactly as the CLI does:
// compose.dev.yml, with Compose's own .env loading disabled.
type dockerCompose struct{ dir string }

func (d dockerCompose) command(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "docker", append([]string{"compose", "--file", "compose.dev.yml"}, args...)...)
	cmd.Dir = d.dir
	cmd.Env = append(slices.DeleteFunc(os.Environ(), func(kv string) bool {
		return strings.HasPrefix(kv, "COMPOSE_ENV_FILES=")
	}), "COMPOSE_DISABLE_ENV_FILE=1")
	return cmd
}

func (d dockerCompose) output(ctx context.Context, args ...string) (string, error) {
	var stdout, stderr bytes.Buffer
	cmd := d.command(ctx, args...)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return "", errors.New(message)
	}
	return stdout.String(), nil
}

func (d dockerCompose) PS(ctx context.Context) ([]composeService, error) {
	out, err := d.output(ctx, "ps", "--all", "--format", "json")
	if err != nil {
		return nil, err
	}
	var services []composeService
	trimmed := strings.TrimSpace(out)
	if strings.HasPrefix(trimmed, "[") {
		err = json.Unmarshal([]byte(trimmed), &services)
	} else {
		for _, line := range strings.Split(trimmed, "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			var service composeService
			if err = json.Unmarshal([]byte(line), &service); err != nil {
				break
			}
			services = append(services, service)
		}
	}
	return services, err
}

func (d dockerCompose) Logs(ctx context.Context, service string, tail int) (string, error) {
	return d.output(ctx, "logs", "--no-color", "--no-log-prefix", "--timestamps", "--tail", fmt.Sprint(tail), service)
}

func (d dockerCompose) Run(ctx context.Context, action, service string) error {
	args := []string{action}
	if service != "" {
		args = append(args, service)
	}
	_, err := d.output(ctx, args...)
	return err
}

type servicesView struct {
	Services []composeService
	Err      string
	Flash    string
}

func (s *Server) loadServices(ctx context.Context) servicesView {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	services, err := s.compose.PS(ctx)
	if err != nil {
		return servicesView{Err: "docker compose is unavailable: " + err.Error()}
	}
	visible := services[:0]
	for _, service := range services {
		if slices.Contains(setupJobs, service.Service) && service.State == "exited" && service.ExitCode == 0 {
			continue
		}
		visible = append(visible, service)
	}
	slices.SortFunc(visible, func(a, b composeService) int { return strings.Compare(a.Service, b.Service) })
	return servicesView{Services: visible}
}

func (s *Server) servicesPage(r *http.Request) templ.Component {
	return servicesPage(s.loadServices(r.Context()))
}

func (s *Server) servicesPanel(w http.ResponseWriter, r *http.Request) {
	render(w, r, servicesPanel(s.loadServices(r.Context())))
}

// knownService guards every compose argument: only services Compose itself
// reports can be targeted, so request input never reaches argv unchecked.
func (s *Server) knownService(ctx context.Context, name string) bool {
	services, err := s.compose.PS(ctx)
	if err != nil {
		return false
	}
	return slices.ContainsFunc(services, func(c composeService) bool { return c.Service == name })
}

func (s *Server) serviceAction(w http.ResponseWriter, r *http.Request) {
	service, action := chi.URLParam(r, "service"), chi.URLParam(r, "action")
	past, ok := map[string]string{"restart": "Restarted", "stop": "Stopped", "start": "Started"}[action]
	if !ok {
		http.NotFound(w, r)
		return
	}
	target := service
	if service == "all" && action == "restart" {
		target = ""
	} else if !s.knownService(r.Context(), service) {
		http.NotFound(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	view := servicesView{}
	if err := s.compose.Run(ctx, action, target); err != nil {
		view = s.loadServices(r.Context())
		view.Err = fmt.Sprintf("Couldn't %s %s: %s", action, service, err)
	} else {
		s.audit(r, past+" service", service, "")
		view = s.loadServices(r.Context())
		view.Flash = fmt.Sprintf("%s: %s requested", service, action)
	}
	render(w, r, servicesPanel(view))
}

type logsView struct {
	Service string
	Lines   []string
	Follow  bool
	Err     string
}

func (s *Server) serviceLogs(w http.ResponseWriter, r *http.Request) {
	service := chi.URLParam(r, "service")
	if !s.knownService(r.Context(), service) {
		http.NotFound(w, r)
		return
	}
	view := logsView{Service: service, Follow: r.URL.Query().Get("follow") != "0"}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	out, err := s.compose.Logs(ctx, service, 200)
	if err != nil {
		view.Err = err.Error()
	} else {
		view.Lines = strings.Split(strings.TrimRight(out, "\n"), "\n")
	}
	render(w, r, logsPanel(view))
}
