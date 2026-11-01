package console

import (
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/a-h/templ"
)

type backup struct {
	Name     string
	Size     int64
	Modified time.Time
}

type backupsView struct {
	Dir     string
	Backups []backup
	Err     string
}

// backupsPage lists the database dumps in server/.misty/backups on this
// machine. It cannot see copies kept elsewhere (an R2 bucket, another host),
// and a listed file is not proof it restores; the page says both.
func (s *Server) backupsPage(*http.Request) templ.Component {
	view := backupsView{Dir: filepath.Join(s.cfg.ServerDir, ".misty", "backups")}
	if s.cfg.ServerDir == "" {
		view.Err = "The console doesn't know the server folder, so it can't look for backups."
		return backupsPage(view)
	}
	entries, err := os.ReadDir(view.Dir)
	if os.IsNotExist(err) {
		return backupsPage(view)
	}
	if err != nil {
		view.Err = "Couldn't read " + view.Dir + ": " + err.Error()
		return backupsPage(view)
	}
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil || entry.IsDir() {
			continue
		}
		view.Backups = append(view.Backups, backup{Name: entry.Name(), Size: info.Size(), Modified: info.ModTime()})
	}
	sort.Slice(view.Backups, func(i, j int) bool { return view.Backups[i].Modified.After(view.Backups[j].Modified) })
	if len(view.Backups) > 30 {
		view.Backups = view.Backups[:30]
	}
	return backupsPage(view)
}

func (v backupsView) newest() string {
	if len(v.Backups) == 0 {
		return "none"
	}
	return ago(v.Backups[0].Modified)
}
