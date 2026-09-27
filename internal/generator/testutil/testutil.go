// Package testutil provides helpers for testing the generators against real packages.
package testutil

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"golang.org/x/mod/modfile"
	"golang.org/x/tools/go/packages"
)

// Module is the module path of the modules created by WriteTree.
const Module = "example.com/datlastest"

// WriteTree creates a temporary Go module holding files and returns its directory.
// The module can import github.com/moznion/go-datlas (e.g. its fixturekit package),
// which resolves to this repository.
func WriteTree(t *testing.T, files map[string]string) string {
	t.Helper()
	_, self, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(self), "..", "..", "..")
	dir := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	if _, ok := files["go.mod"]; !ok {
		files["go.mod"], files["go.sum"] = testModule(t, root)
	}
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// testModule returns the go.mod and go.sum of a module requiring the datlas module at root.
// With module graph pruning, the requirements of datlas must be listed too; they are
// copied from its go.mod, and its go.sum covers them.
func testModule(t *testing.T, root string) (string, string) {
	t.Helper()
	src, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	mf, err := modfile.Parse("go.mod", src, nil)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "module %s\n\ngo %s\n\nrequire (\n\tgithub.com/moznion/go-datlas v0.0.0\n", Module, mf.Go.Version)
	for _, r := range mf.Require {
		fmt.Fprintf(&b, "\t%s %s // indirect\n", r.Mod.Path, r.Mod.Version)
	}
	fmt.Fprintf(&b, ")\n\nreplace github.com/moznion/go-datlas => %s\n", filepath.ToSlash(root))
	sum, err := os.ReadFile(filepath.Join(root, "go.sum"))
	if err != nil {
		t.Fatal(err)
	}
	return b.String(), string(sum)
}

// TypeCheck fails the test when the packages matched by patterns do not type-check.
func TypeCheck(t *testing.T, dir string, patterns ...string) {
	t.Helper()
	pkgs, err := packages.Load(&packages.Config{Mode: packages.LoadAllSyntax, Dir: dir}, patterns...)
	if err != nil {
		t.Fatal(err)
	}
	var msgs []string
	packages.Visit(pkgs, nil, func(p *packages.Package) {
		for _, e := range p.Errors {
			msgs = append(msgs, e.Error())
		}
	})
	if len(msgs) > 0 {
		t.Fatalf("generated code does not type-check:\n%s", strings.Join(msgs, "\n"))
	}
}

// ReadFile returns the content of path or fails the test.
func ReadFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
