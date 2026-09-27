package output

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestImports(t *testing.T) {
	im := NewImports("example.com/self", "time")
	if got := im.Add("example.com/self", "self"); got != "" {
		t.Fatalf("self import got alias %q", got)
	}
	if got := im.Add("time", "time"); got != "time2" {
		t.Fatalf("reserved name not avoided: %q", got)
	}
	if got := im.Add("example.com/a/model", "model"); got != "model" {
		t.Fatalf("got %q", got)
	}
	if got := im.Add("example.com/b/model", "model"); got != "model2" {
		t.Fatalf("colliding name not aliased: %q", got)
	}
	if got := im.Add("example.com/a/model", "model"); got != "model" {
		t.Fatalf("alias not reused: %q", got)
	}
	want := "import (\n\ttime2 \"time\"\n\n\t\"example.com/a/model\"\n\tmodel2 \"example.com/b/model\"\n)\n"
	if got := im.Decl(); got != want {
		t.Fatalf("Decl() =\n%s\nwant\n%s", got, want)
	}
	if NewImports("x").Decl() != "" {
		t.Fatal("empty import set rendered a declaration")
	}
}

func TestDefaultName(t *testing.T) {
	for path, want := range map[string]string{
		"time":                     "time",
		"github.com/goccy/go-yaml": "go-yaml",
		"example.com/mod/v2":       "mod",
		"github.com/google/uuid":   "uuid",
	} {
		if got := defaultName(path); got != want {
			t.Errorf("defaultName(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestApply(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "zz_gen.go")
	src := File("p", NewImports("p"), []byte("var X   = 1\n"))

	if _, err := Apply(Result{Path: path, Src: src}, true); err == nil || !strings.Contains(err.Error(), "out of date") {
		t.Fatalf("check of a missing file: %v", err)
	}
	if msg, err := Apply(Result{Path: path, Src: src}, false); err != nil || !strings.HasPrefix(msg, "wrote ") {
		t.Fatalf("write: %q, %v", msg, err)
	}
	if got, _ := os.ReadFile(path); !strings.Contains(string(got), "var X = 1") {
		t.Fatalf("file not formatted:\n%s", got)
	}
	if _, err := Apply(Result{Path: path, Src: src}, true); err != nil {
		t.Fatalf("check of an up-to-date file: %v", err)
	}
	changed := File("p", NewImports("p"), []byte("var X = 2\n"))
	if _, err := Apply(Result{Path: path, Src: changed}, true); err == nil {
		t.Fatal("check did not detect a change")
	}
	if got, _ := os.ReadFile(path); !strings.Contains(string(got), "var X = 1") {
		t.Fatal("check mode modified the file")
	}
	if msg, err := Apply(Result{Path: path}, false); err != nil || !strings.HasPrefix(msg, "removed stale ") {
		t.Fatalf("remove: %q, %v", msg, err)
	}
	if msg, err := Apply(Result{Path: path}, true); err != nil || msg != "" {
		t.Fatalf("removing a missing file: %q, %v", msg, err)
	}

	handwritten := filepath.Join(dir, "hand.go")
	if err := os.WriteFile(handwritten, []byte("package p\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, r := range []Result{{Path: handwritten, Src: src}, {Path: handwritten}} {
		if _, err := Apply(r, false); err == nil || !strings.Contains(err.Error(), "refusing to modify") {
			t.Fatalf("handwritten file: %v", err)
		}
	}
	if _, err := Apply(Result{Path: path, Src: []byte("package p\nfunc (")}, false); err == nil {
		t.Fatal("invalid source was written")
	}
}
