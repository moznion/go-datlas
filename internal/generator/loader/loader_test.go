package loader_test

import (
	"strings"
	"testing"

	"github.com/moznion/go-datlas/internal/generator/loader"
	"github.com/moznion/go-datlas/internal/generator/testutil"
)

func TestDirectives(t *testing.T) {
	dir := testutil.WriteTree(t, map[string]string{
		"p/p.go": `package p

// User is documented.
//
//datlas:fixture
type User struct{}

type (
	//datlas:master roles
	Role struct{}

	//datlas:enum
	Color int
)
`,
	})
	pkgs, err := loader.Load(dir, "./p")
	if err != nil {
		t.Fatal(err)
	}
	ds, err := loader.Directives(pkgs)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, d := range ds {
		got = append(got, d.Kind+":"+d.Type.Name()+":"+strings.Join(d.Args, ","))
	}
	if want := "fixture:User:,master:Role:roles,enum:Color:"; strings.Join(got, ",") != want {
		t.Fatalf("got %v, want %s", got, want)
	}
}

func TestDirectiveErrors(t *testing.T) {
	for src, want := range map[string]string{
		"package p\n\n//datlas:unknown\ntype T struct{}\n":                               "unknown datlas directive",
		"package p\n\n//datlas:fixture\ntype T[X any] struct{}\n":                        "cannot be used on generic types",
		"package p\n\n//datlas:fixture\ntype T int\n":                                    "can only be used on struct types",
		"package p\n\n//datlas:enum x\ntype T int\n":                                     "takes no arguments",
		"package p\n\n//datlas:enum\ntype T = int\n":                                     "defined string or integer types",
		"package p\n\nfunc f() {\n\t//datlas:fixture\n\ttype T struct{}\n\t_ = T{}\n}\n": "must be placed on a type declaration",
	} {
		dir := testutil.WriteTree(t, map[string]string{"p/p.go": src})
		pkgs, err := loader.Load(dir, "./p")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := loader.Directives(pkgs); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: error %v does not contain %q", src, err, want)
		}
	}
}

func TestLoadToleratesTypeErrorsButNotSyntaxErrors(t *testing.T) {
	dir := testutil.WriteTree(t, map[string]string{
		"ok/use.go":  "package ok\n\nvar _ = Generated\n",
		"bad/bad.go": "package bad\n\nfunc (\n",
	})
	if _, err := loader.Load(dir, "./ok"); err != nil {
		t.Fatalf("type errors must be tolerated: %v", err)
	}
	if _, err := loader.Load(dir, "./bad"); err == nil {
		t.Fatal("syntax errors must be reported")
	}
	if _, err := loader.Load(dir, "./missing"); err == nil {
		t.Fatal("missing packages must be reported")
	}
}
