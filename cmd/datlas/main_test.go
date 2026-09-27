package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moznion/go-datlas/internal/generator/testutil"
)

func TestCommands(t *testing.T) {
	var out, errOut bytes.Buffer
	if err := run([]string{"help"}, &out, &errOut); err != nil || !strings.Contains(out.String(), "datlas fixture") {
		t.Fatalf("help: %v %q", err, out.String())
	}
	out.Reset()
	if err := run([]string{"version"}, &out, &errOut); err != nil || !strings.HasPrefix(out.String(), "datlas ") {
		t.Fatalf("version: %v %q", err, out.String())
	}
	if err := run(nil, &out, &errOut); err == nil {
		t.Fatal("missing command accepted")
	}
	if err := run([]string{"nope"}, &out, &errOut); err == nil || !strings.Contains(err.Error(), "unknown command") {
		t.Fatalf("unknown command: %v", err)
	}
	errOut.Reset()
	if err := run([]string{"master", "-h"}, &out, &errOut); err == nil || !strings.Contains(errOut.String(), "Usage: datlas master") || !strings.Contains(errOut.String(), "-check") {
		t.Fatalf("command help: %v %q", err, errOut.String())
	}
	if err := run([]string{"seed", "a", "b"}, &out, &errOut); err == nil {
		t.Fatal("seed with two packages accepted")
	}
}

func TestCheckMode(t *testing.T) {
	dir := testutil.WriteTree(t, map[string]string{
		"data/schema.go":  "package data\n\n//datlas:master roles\ntype Role struct {\n\tCode string `master:\"key\"`\n}\n",
		"data/roles.yaml": "- code: a\n",
	})
	t.Chdir(filepath.Join(dir, "data"))
	var out, errOut bytes.Buffer
	if err := run([]string{"master", "-check"}, &out, &errOut); err == nil || !strings.Contains(err.Error(), "out of date") {
		t.Fatalf("check before generation: %v", err)
	}
	if err := run([]string{"master"}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"master", "-check"}, &out, &errOut); err != nil {
		t.Fatalf("check after generation: %v", err)
	}
	if err := os.WriteFile("roles.yaml", []byte("- code: b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"master", "-check"}, &out, &errOut); err == nil {
		t.Fatal("check did not detect changed YAML")
	}
}

func TestFixtureSkeletonAndConfig(t *testing.T) {
	dir := testutil.WriteTree(t, map[string]string{
		"gen/gen.go":     "package gen\n\ntype User struct {\n\tName string\n\tNote string\n}\n",
		"fx/doc.go":      "package fx\n",
		"fx/policy.yaml": "types:\n  - type: " + testutil.Module + "/gen.User\n    fields:\n      Name: name\n      Note: \"-\"\n",
	})
	t.Chdir(filepath.Join(dir, "fx"))
	var out, errOut bytes.Buffer
	if err := run([]string{"fixture"}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	if src := testutil.ReadFile(t, "zz_fixture_gen.go"); !strings.Contains(src, "type Factory struct") {
		t.Fatalf("skeleton was not generated:\n%s", src)
	}
	if err := run([]string{"fixture", "-config", "policy.yaml"}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	if src := testutil.ReadFile(t, "zz_fixture_gen.go"); !strings.Contains(src, "func (f *Factory) User(") {
		t.Fatalf("policy file was not used:\n%s", src)
	}
	testutil.TypeCheck(t, dir, "./...")
}
