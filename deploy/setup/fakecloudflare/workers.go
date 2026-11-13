package fakecloudflare

import (
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
)

func (f *Fake) serveWorkers(w http.ResponseWriter, r *http.Request, parts []string) {
	switch {
	case len(parts) == 1 && parts[0] == "subdomain" && r.Method == "GET":
		if f.Subdomain == "" {
			reply(w, http.StatusNotFound, nil)
			return
		}
		reply(w, 200, map[string]any{"subdomain": f.Subdomain})
	case len(parts) == 1 && parts[0] == "scripts" && r.Method == "GET":
		scripts := []map[string]any{}
		for name, script := range f.Scripts {
			scripts = append(scripts, map[string]any{"id": name, "migration_tag": script.MigrationTag})
		}
		reply(w, 200, scripts)
	case len(parts) == 2 && parts[0] == "scripts" && r.Method == "PUT":
		f.uploadScript(w, r, parts[1])
	case len(parts) == 3 && parts[0] == "scripts" && parts[2] == "subdomain" && r.Method == "POST" && f.Scripts[parts[1]] != nil:
		f.Scripts[parts[1]].Subdomain = decode(r)["enabled"] == true
		reply(w, 200, map[string]any{"enabled": f.Scripts[parts[1]].Subdomain})
	default:
		reply(w, http.StatusNotFound, nil)
	}
}

func (f *Fake) uploadScript(w http.ResponseWriter, r *http.Request, name string) {
	_, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		reply(w, http.StatusBadRequest, nil)
		return
	}
	reader := multipart.NewReader(r.Body, params["boundary"])
	script := f.Scripts[name]
	if script == nil {
		script = &Script{}
	}
	upload := &Script{Modules: map[string][]byte{}, MigrationTag: script.MigrationTag, Subdomain: script.Subdomain, Uploads: script.Uploads + 1}
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			reply(w, http.StatusBadRequest, nil)
			return
		}
		data, _ := io.ReadAll(part)
		if part.FormName() == "metadata" {
			if json.Unmarshal(data, &upload.Metadata) != nil {
				reply(w, http.StatusBadRequest, nil)
				return
			}
			continue
		}
		upload.Modules[part.FormName()] = data
	}
	if upload.Metadata == nil || upload.Modules[fmt.Sprint(upload.Metadata["main_module"])] == nil {
		reply(w, http.StatusBadRequest, nil)
		return
	}
	if migrations, ok := upload.Metadata["migrations"].(map[string]any); ok {
		if old, _ := migrations["old_tag"].(string); old != script.MigrationTag {
			reply(w, http.StatusBadRequest, nil)
			return
		}
		upload.MigrationTag, _ = migrations["new_tag"].(string)
	}
	f.Scripts[name] = upload
	reply(w, 200, map[string]any{"id": name})
}
