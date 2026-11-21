package architecture

import (
	"bufio"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/misty-org/misty-server/internal/platform/serverenv"
)

func TestRepositoryLayout(t *testing.T) {
	root := repositoryRoot(t)
	for _, name := range []string{"accounts", "spaces"} {
		requireDirectory(t, filepath.Join(root, "internal", name))
	}
	for _, legacy := range []string{"api", "db", "agent", "billing", "workflow"} {
		if _, err := os.Stat(filepath.Join(root, legacy)); !os.IsNotExist(err) {
			t.Errorf("legacy top-level package %q still exists", legacy)
		}
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".go") {
			t.Errorf("repository root contains Go file %q", entry.Name())
		}
	}
}

// Internal unit tests may exercise unexported state machines beside production
// code. TestDomainImportDirection checks that deployed packages never import tests.
func TestHandwrittenGoFilesRespectHardMaximum(t *testing.T) {
	root := repositoryRoot(t)
	walkGoFiles(t, root, func(relative, absolute string) {
		file, err := os.Open(absolute)
		if err != nil {
			t.Error(err)
			return
		}
		defer file.Close()

		scanner := bufio.NewScanner(file)
		lines := 0
		generated := false
		for scanner.Scan() {
			lines++
			if lines <= 10 && strings.HasPrefix(scanner.Text(), "// Code generated ") &&
				strings.HasSuffix(scanner.Text(), " DO NOT EDIT.") {
				generated = true
			}
		}
		if err := scanner.Err(); err != nil {
			t.Error(err)
		}
		if !generated && lines > 500 {
			t.Errorf("%s has %d lines; hard maximum is 500", relative, lines)
		}
	})
}

func TestEnvironmentAccessIsCentralized(t *testing.T) {
	root := repositoryRoot(t)
	files := token.NewFileSet()
	walkGoFiles(t, root, func(relative, absolute string) {
		if strings.HasSuffix(relative, "_test.go") || strings.HasPrefix(relative, "cmd/") ||
			strings.HasPrefix(relative, "internal/platform/config/") {
			return
		}
		parsed, err := parser.ParseFile(files, absolute, nil, 0)
		if err != nil {
			t.Errorf("parse %s: %v", relative, err)
			return
		}
		ast.Inspect(parsed, func(node ast.Node) bool {
			selector, ok := node.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			identifier, ok := selector.X.(*ast.Ident)
			if ok && identifier.Name == "os" &&
				(selector.Sel.Name == "Getenv" || selector.Sel.Name == "LookupEnv") {
				t.Errorf("direct environment access outside config/cmd: %s", relative)
			}
			return true
		})
	})
}

// environmentContract is every environment variable the server reads. A
// server's values come from ~/.misty/.env/server/ and the setup container;
// no env file is committed. On a stack setup manages, the keys come from
// MISTY_API_SECRET and MISTY_SERVER_KEY_VERSIONS, and MISTY_PUBLIC_API_URL and
// R2_BUCKET from MISTY_HOSTNAME (config.ApplyServerDefaults); a value set
// under its own name still wins. serverEnvironmentContract lists what the
// server reads only through that derivation.
const environmentContract = `
CLOUDFLARE_API_TOKEN
LIVEKIT_API_KEY
LIVEKIT_API_SECRET
LIVEKIT_URL
DB_CONN_MAX_IDLE_TIME
DB_CONN_MAX_LIFETIME
DB_HOST
DB_MAX_IDLE_CONNS
DB_MAX_OPEN_CONNS
DB_NAME
DB_PASSWORD
DB_PORT
DB_USER
DOCUMENT_SIGNING_KEY
ELEVENLABS_API_KEY
GOOGLE_CLIENT_ID
GOOGLE_CLIENT_SECRET
GOOGLE_SIGN_IN_REDIRECT_URL
JOURNAL_COLLAB_CONTROL_SECRET
JOURNAL_COLLAB_PROJECTION_SECRET
JOURNAL_COLLAB_PROJECTION_SECRET_PREVIOUS
JOURNAL_COLLAB_ROOM_SALT
JOURNAL_COLLAB_TICKET_PRIVATE_KEY
MISTY_AGENT_MODEL
MISTY_AI_MODEL_CATALOG_JSON
MAILJET_API_BASE_URL
MAILJET_API_KEY
MAILJET_FROM_EMAIL
MAILJET_FROM_NAME
MAILJET_SECRET_KEY
MISTY_ALLOWED_ORIGINS
MISTY_AUTH_SIGNING_KEY
MISTY_AUTH_SIGNING_KEY_PREVIOUS
MISTY_CHAT_ATTACHMENT_MAX_FILE_BYTES
MISTY_EGRESS_MAX_BYTES_PER_DAY
MISTY_EGRESS_MAX_BYTES_PER_IDENTITY_DAY
MISTY_ENVIRONMENT
MISTY_HOSTNAME
MISTY_METRICS_TOKEN
MISTY_NOTE_ATTACHMENT_MAX_FILE_BYTES
MISTY_ALLOWED_EMAILS
MISTY_OPEN_SIGNUP
MISTY_OPERATOR_TOKEN
MISTY_OWNER_EMAIL
MISTY_PUBLIC_API_URL
MISTY_R2_DOWNLOAD_URL_TTL
MISTY_R2_UPLOAD_URL_TTL
MISTY_RECALL
MISTY_RECALL_BACKLOG_CAP
MISTY_RELEASE_CHANNEL
MISTY_SIGNUP_LIMITS
MISTY_SPACE_LEVELS
MISTY_WEBSITE_URL
PARTYKIT_HOST
PORT
R2_ACCESS_KEY
R2_BUCKET
R2_ENDPOINT
R2_SECRET_KEY
SPACE_LINK_ENCRYPTION_KEY
TRUSTED_PROXY_CIDRS
TRUST_PROXY_HEADERS
`

// serverEnvironmentContract is read by serverenv.Defaults, through the
// getenv it is given rather than config.Getenv.
const serverEnvironmentContract = `
MISTY_API_SECRET
MISTY_SERVER_KEY_VERSIONS
`

func TestEnvironmentContractIsExplicit(t *testing.T) {
	root := repositoryRoot(t)
	documented := map[string]bool{}
	for _, name := range strings.Fields(environmentContract + serverEnvironmentContract) {
		documented[name] = true
	}

	files := token.NewFileSet()
	used := map[string]bool{}
	walkGoFiles(t, filepath.Join(root, "internal"), func(relative, absolute string) {
		parsed, err := parser.ParseFile(files, absolute, nil, 0)
		if err != nil {
			t.Errorf("parse %s: %v", relative, err)
			return
		}
		ast.Inspect(parsed, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			identifier, isIdentifier := selector.X.(*ast.Ident)
			literal, isLiteral := call.Args[0].(*ast.BasicLit)
			if !isIdentifier || !isLiteral || (identifier.Name != "config" && identifier.Name != "envconfig") ||
				(selector.Sel.Name != "Getenv" && selector.Sel.Name != "LookupEnv") {
				return true
			}
			name, err := strconv.Unquote(literal.Value)
			if err == nil {
				used[name] = true
			}
			return true
		})
	})
	for name := range used {
		if !documented[name] {
			t.Errorf("environment variable %s is missing from the environment contract", name)
		}
	}
}

// Every key the API derives is one it reads, and the derivation's own
// variables are the ones listed.
func TestDerivedKeysAreInTheEnvironmentContract(t *testing.T) {
	documented := map[string]bool{}
	for _, name := range strings.Fields(environmentContract) {
		documented[name] = true
	}
	for _, key := range serverenv.Keys {
		if key.Scope == serverenv.FromAPISecret && !documented[key.Name] {
			t.Errorf("the API derives %s but the environment contract does not list it", key.Name)
		}
	}
	if strings.Join(strings.Fields(serverEnvironmentContract), " ") != serverenv.APISecretName+" "+serverenv.VersionsName {
		t.Errorf("serverEnvironmentContract must list %s and %s", serverenv.APISecretName, serverenv.VersionsName)
	}
}

func TestDomainImportDirection(t *testing.T) {
	root := repositoryRoot(t)
	command := exec.Command("go", "list", "-json", "./internal/...")
	command.Dir = root
	output, err := command.Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}

	decoder := json.NewDecoder(strings.NewReader(string(output)))
	for {
		var pkg struct {
			ImportPath string
			Imports    []string
		}
		if err := decoder.Decode(&pkg); err != nil {
			if err == io.EOF {
				break
			}
			t.Fatal(err)
		}
		if strings.Contains(pkg.ImportPath, "/internal/") {
			for _, imported := range pkg.Imports {
				if strings.Contains(imported, "/test/") {
					t.Errorf("production package %s imports test package %s", pkg.ImportPath, imported)
				}
			}
		}
		if !isIndependentDomain(pkg.ImportPath) {
			continue
		}
		for _, imported := range pkg.Imports {
			if strings.Contains(imported, "/internal/app") ||
				strings.Contains(imported, "/internal/platform/httpapi") ||
				strings.Contains(imported, "/internal/platform/db") {
				t.Errorf("domain package %s imports concrete adapter %s", pkg.ImportPath, imported)
			}
		}
	}
}

func isIndependentDomain(importPath string) bool {
	for _, domain := range []string{"accounts", "spaces", "journal", "library", "discovery", "integrations", "agent", "workflow"} {
		if strings.HasSuffix(importPath, "/internal/"+domain) || strings.Contains(importPath, "/internal/"+domain+"/") {
			return true
		}
	}
	return false
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate architecture test")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", "..", ".."))
}

func requireDirectory(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		t.Errorf("required directory missing: %s", path)
	}
}

func walkGoFiles(t *testing.T, root string, visit func(relative, absolute string)) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// `.claude` holds local agent worktrees: copies of the repository that git ignores and
		// that aren't part of this checkout's source.
		if entry.IsDir() && (entry.Name() == ".git" || entry.Name() == ".claude" ||
			entry.Name() == "node_modules" || entry.Name() == "vendor") {
			return filepath.SkipDir
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		visit(filepath.ToSlash(relative), path)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
