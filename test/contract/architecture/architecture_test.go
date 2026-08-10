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
)

func TestRepositoryLayout(t *testing.T) {
	root := repositoryRoot(t)
	for _, name := range []string{"accounts", "billing", "spaces", "journal", "library", "discovery", "agents", "workflows", "integrations"} {
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

func TestProductionDirectoriesContainNoTests(t *testing.T) {
	root := repositoryRoot(t)
	walkGoFiles(t, root, func(relative, absolute string) {
		if strings.HasSuffix(relative, "_test.go") && !strings.HasPrefix(relative, "test/") {
			t.Errorf("test file must live under test/: %s", relative)
		}
	})
}

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
		if strings.HasPrefix(relative, "cmd/") ||
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

func TestEnvironmentContractIsExplicit(t *testing.T) {
	root := repositoryRoot(t)
	documented := map[string]bool{}
	example, err := os.ReadFile(filepath.Join(root, "test", "contract", "architecture", "fixtures", "environment-contract.env"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(example), "\n") {
		name, _, found := strings.Cut(line, "=")
		if found && name != "" && strings.ToUpper(name) == name {
			documented[name] = true
		}
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
			if !isIdentifier || !isLiteral || identifier.Name != "envconfig" {
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
				strings.Contains(imported, "/internal/platform/postgres") {
				t.Errorf("domain package %s imports concrete adapter %s", pkg.ImportPath, imported)
			}
		}
	}
}

func isIndependentDomain(importPath string) bool {
	for _, domain := range []string{"accounts", "spaces", "journal", "library", "discovery", "integrations", "agents", "workflows"} {
		if strings.Contains(importPath, "/internal/"+domain) {
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
		if entry.IsDir() && (entry.Name() == ".git" || entry.Name() == "node_modules" || entry.Name() == "vendor") {
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
