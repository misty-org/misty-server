package console

import (
	"encoding/json"
	"net/http"
	"os"

	"github.com/a-h/templ"

	"github.com/misty-org/misty-server/internal/platform/db"
)

// envSchemaEntry is one variable in the schema file the CLI writes from its
// environment registry (misty-cli's src/environment/registry.rs).
type envSchemaEntry struct {
	File     string `json:"file"`
	Name     string `json:"name"`
	Required bool   `json:"required"`
}

type settingGroup struct {
	File     string
	Settings []setting
}

type configurationView struct {
	Groups  []settingGroup
	Missing int
	Err     string
}

func (s *Server) configurationPage(*http.Request) templ.Component {
	view := configurationView{}
	if s.cfg.EnvSchemaPath == "" {
		view.Err = "Start the console with misty server up --gui to list known settings."
		return configurationPage(view)
	}
	data, err := os.ReadFile(s.cfg.EnvSchemaPath)
	var entries []envSchemaEntry
	if err == nil {
		err = json.Unmarshal(data, &entries)
	}
	if err != nil {
		view.Err = "Couldn't read the settings schema: " + err.Error()
		return configurationPage(view)
	}
	for _, entry := range entries {
		if len(view.Groups) == 0 || view.Groups[len(view.Groups)-1].File != entry.File {
			view.Groups = append(view.Groups, settingGroup{File: entry.File})
		}
		item := s.setting(entry.Name, entry.Required)
		if item.Required && !item.Set {
			view.Missing++
		}
		group := &view.Groups[len(view.Groups)-1]
		group.Settings = append(group.Settings, item)
	}
	return configurationPage(view)
}

type databaseView struct {
	Stats   db.ConsoleDatabaseStats
	Pending int
	Err     string
}

func (s *Server) databasePage(r *http.Request) templ.Component {
	if s.db == nil {
		return databaseUnavailable("Database")
	}
	view := databaseView{}
	stats, err := s.db.ConsoleDatabaseStats(r.Context())
	if err != nil {
		view.Err = "Couldn't read database statistics: " + err.Error()
	}
	view.Stats = stats
	for _, migration := range stats.Migrations {
		if !migration.Applied {
			view.Pending++
		}
	}
	return databasePage(view)
}

func (v databaseView) recentMigrations() []db.ConsoleMigration {
	if len(v.Stats.Migrations) > 8 {
		return v.Stats.Migrations[:8]
	}
	return v.Stats.Migrations
}
