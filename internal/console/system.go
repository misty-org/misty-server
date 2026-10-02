package console

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/a-h/templ"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

// envSchemaEntry is one variable in the schema file the CLI writes from its
// environment registry (cli/src/environment.rs).
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

type backup struct {
	Name     string
	Modified time.Time
}

type databaseView struct {
	Stats   db.ConsoleDatabaseStats
	Pending int
	Backups []backup
	Err     string
}

func (s *Server) databasePage(r *http.Request) templ.Component {
	if s.db == nil {
		return databaseUnavailable("Database")
	}
	view := databaseView{Backups: s.listBackups()}
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

// listBackups returns the newest entries in server/.misty/backups.
func (s *Server) listBackups() []backup {
	if s.cfg.ServerDir == "" {
		return nil
	}
	entries, err := os.ReadDir(filepath.Join(s.cfg.ServerDir, ".misty", "backups"))
	if err != nil {
		return nil
	}
	var backups []backup
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}
		backups = append(backups, backup{Name: entry.Name(), Modified: info.ModTime()})
	}
	sort.Slice(backups, func(i, j int) bool { return backups[i].Modified.After(backups[j].Modified) })
	if len(backups) > 10 {
		backups = backups[:10]
	}
	return backups
}

func (v databaseView) recentMigrations() []db.ConsoleMigration {
	if len(v.Stats.Migrations) > 8 {
		return v.Stats.Migrations[:8]
	}
	return v.Stats.Migrations
}
