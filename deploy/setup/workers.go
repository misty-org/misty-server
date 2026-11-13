package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/textproto"
	"os"
	"path/filepath"
	"sort"
)

// The Workers ship prebuilt in the setup image (wrangler's dry-run bundle and
// its wrangler.jsonc), so setup uploads them through the API with no Node or
// wrangler at run time.

type wranglerConfig struct {
	CompatibilityDate  string            `json:"compatibility_date"`
	CompatibilityFlags []string          `json:"compatibility_flags"`
	Vars               map[string]string `json:"vars"`
	Observability      *struct {
		Enabled bool `json:"enabled"`
	} `json:"observability"`
	DurableObjects struct {
		Bindings []struct {
			Name      string `json:"name"`
			ClassName string `json:"class_name"`
		} `json:"bindings"`
	} `json:"durable_objects"`
	Migrations []map[string]any `json:"migrations"`
}

type workerModule struct {
	Name, ContentType string
	Data              []byte
}

type workerBundle struct {
	Config  wranglerConfig
	Modules []workerModule
}

func loadWorkerBundle(dir string) (workerBundle, error) {
	var bundle workerBundle
	data, err := os.ReadFile(filepath.Join(dir, "wrangler.jsonc"))
	if err != nil {
		return bundle, err
	}
	if err := decodeJSONC(data, &bundle.Config); err != nil {
		return bundle, fmt.Errorf("%s/wrangler.jsonc: %w", dir, err)
	}
	entries, err := os.ReadDir(filepath.Join(dir, "dist"))
	if err != nil {
		return bundle, err
	}
	for _, entry := range entries {
		contentType := ""
		switch filepath.Ext(entry.Name()) {
		case ".js", ".mjs":
			contentType = "application/javascript+module"
		case ".wasm":
			contentType = "application/wasm"
		}
		if contentType == "" || entry.IsDir() {
			continue
		}
		module, err := os.ReadFile(filepath.Join(dir, "dist", entry.Name()))
		if err != nil {
			return bundle, err
		}
		bundle.Modules = append(bundle.Modules, workerModule{Name: entry.Name(), ContentType: contentType, Data: module})
	}
	sort.Slice(bundle.Modules, func(i, j int) bool { return bundle.Modules[i].Name < bundle.Modules[j].Name })
	if !hasModule(bundle, "index.js") {
		return bundle, fmt.Errorf("%s/dist has no index.js", dir)
	}
	return bundle, nil
}

func hasModule(bundle workerBundle, name string) bool {
	for _, module := range bundle.Modules {
		if module.Name == name {
			return true
		}
	}
	return false
}

// workerDeploy is one Worker with the values this server gives it.
type workerDeploy struct {
	Name    string
	Bundle  workerBundle
	Vars    map[string]string
	Secrets map[string]string
}

// metadata builds the upload metadata. migrationTag is the tag the deployed
// script already has ("" for a new script).
func (w workerDeploy) metadata(migrationTag string) (map[string]any, error) {
	config := w.Bundle.Config
	var bindings []map[string]any
	vars := map[string]string{}
	for name, value := range config.Vars {
		vars[name] = value
	}
	for name, value := range w.Vars {
		vars[name] = value
	}
	for _, name := range sortedKeys(vars) {
		bindings = append(bindings, map[string]any{"type": "plain_text", "name": name, "text": vars[name]})
	}
	for _, name := range sortedKeys(w.Secrets) {
		if w.Secrets[name] != "" {
			bindings = append(bindings, map[string]any{"type": "secret_text", "name": name, "text": w.Secrets[name]})
		}
	}
	for _, object := range config.DurableObjects.Bindings {
		bindings = append(bindings, map[string]any{"type": "durable_object_namespace", "name": object.Name, "class_name": object.ClassName})
	}
	metadata := map[string]any{
		"main_module":         "index.js",
		"compatibility_date":  config.CompatibilityDate,
		"compatibility_flags": config.CompatibilityFlags,
		"bindings":            bindings,
	}
	if config.Observability != nil {
		metadata["observability"] = map[string]any{"enabled": config.Observability.Enabled}
	}
	if migrations, err := pendingMigrations(config.Migrations, migrationTag); err != nil {
		return nil, err
	} else if migrations != nil {
		metadata["migrations"] = migrations
	}
	return metadata, nil
}

// pendingMigrations returns the Durable Object migration steps after the
// deployed tag, or nil when the script is current.
func pendingMigrations(all []map[string]any, deployed string) (map[string]any, error) {
	if len(all) == 0 {
		return nil, nil
	}
	start := 0
	if deployed != "" {
		start = -1
		for index, migration := range all {
			if migration["tag"] == deployed {
				start = index + 1
			}
		}
		if start < 0 {
			return nil, fmt.Errorf("the deployed Worker has migration tag %q, which this version does not know", deployed)
		}
	}
	if start == len(all) {
		return nil, nil
	}
	var steps []map[string]any
	for _, migration := range all[start:] {
		step := map[string]any{}
		for key, value := range migration {
			if key != "tag" {
				step[key] = value
			}
		}
		steps = append(steps, step)
	}
	out := map[string]any{"new_tag": all[len(all)-1]["tag"], "steps": steps}
	if deployed != "" {
		out["old_tag"] = deployed
	}
	return out, nil
}

// fingerprint changes whenever the code, configuration or a secret changes,
// so setup uploads only then.
func (w workerDeploy) fingerprint() string {
	hash := sha256.New()
	metadata, _ := w.metadata("")
	data, _ := json.Marshal(metadata)
	hash.Write(data)
	for _, module := range w.Bundle.Modules {
		hash.Write([]byte(module.Name))
		hash.Write(module.Data)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func sortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// deployWorker uploads the script when it changed and keeps it on workers.dev.
func deployWorker(ctx context.Context, cf *cloudflare, store *Store, worker workerDeploy) error {
	var scripts []struct {
		ID           string `json:"id"`
		MigrationTag string `json:"migration_tag"`
	}
	if err := cf.call(ctx, "GET", cf.accountPath("workers/scripts"), nil, &scripts); err != nil {
		return err
	}
	exists, migrationTag := false, ""
	for _, script := range scripts {
		if script.ID == worker.Name {
			exists, migrationTag = true, script.MigrationTag
		}
	}
	fingerprintName := "worker:" + worker.Name
	fingerprint := worker.fingerprint()
	if !exists || store.State[fingerprintName] != fingerprint {
		metadata, err := worker.metadata(migrationTag)
		if err != nil {
			return err
		}
		body, contentType, err := workerUploadBody(metadata, worker.Bundle.Modules)
		if err != nil {
			return err
		}
		if err := cf.send(ctx, "PUT", cf.accountPath("workers/scripts/%s", worker.Name), body, contentType, nil); err != nil {
			return err
		}
		store.State[fingerprintName] = fingerprint
	}
	return cf.call(ctx, "POST", cf.accountPath("workers/scripts/%s/subdomain", worker.Name), map[string]any{"enabled": true, "previews_enabled": false}, nil)
}

func workerUploadBody(metadata map[string]any, modules []workerModule) (*bytes.Buffer, string, error) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	data, err := json.Marshal(metadata)
	if err != nil {
		return nil, "", err
	}
	if err := writer.WriteField("metadata", string(data)); err != nil {
		return nil, "", err
	}
	for _, module := range modules {
		header := textproto.MIMEHeader{}
		header.Set("Content-Disposition", fmt.Sprintf(`form-data; name=%q; filename=%q`, module.Name, module.Name))
		header.Set("Content-Type", module.ContentType)
		part, err := writer.CreatePart(header)
		if err != nil {
			return nil, "", err
		}
		if _, err := part.Write(module.Data); err != nil {
			return nil, "", err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, "", err
	}
	return body, writer.FormDataContentType(), nil
}
