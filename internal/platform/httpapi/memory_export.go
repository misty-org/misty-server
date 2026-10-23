package api

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/kannachi323/misty/server/internal/platform/db"
)

// MemoryExport downloads everything Memory kept as a zip: memory.jsonl with
// one event per line, and a readable Markdown page per day.
func (s *SpacesService) MemoryExport() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := s.memoryUser(w, r)
		if !ok {
			return
		}
		days := map[string]*strings.Builder{}
		order := []string{}
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", `attachment; filename="misty-memory-`+time.Now().UTC().Format("2006-01-02")+`.zip"`)
		archive := zip.NewWriter(w)
		lines, err := archive.Create("memory.jsonl")
		if err == nil {
			encoder := json.NewEncoder(lines)
			err = s.database.EachMemoryEvent(r.Context(), user, func(event db.MemoryEvent) error {
				day := event.OccurredAt.UTC().Format("2006-01-02")
				page := days[day]
				if page == nil {
					page = &strings.Builder{}
					days[day], order = page, append(order, day)
					fmt.Fprintf(page, "# %s\n\n", day)
				}
				fmt.Fprintf(page, "- %s · %s", event.OccurredAt.UTC().Format("15:04"), firstNonEmpty(event.Title, event.URL, event.Kind))
				if event.URL != "" {
					fmt.Fprintf(page, " — %s", event.URL)
				}
				page.WriteString("\n")
				return encoder.Encode(event)
			})
		}
		for _, day := range order {
			if err != nil {
				break
			}
			var file interface{ Write([]byte) (int, error) }
			if file, err = archive.Create("days/" + day + ".md"); err == nil {
				_, err = file.Write([]byte(days[day].String()))
			}
		}
		// Headers are already sent; a broken stream is the only signal left.
		if closeErr := archive.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			panic(http.ErrAbortHandler)
		}
	}
}
