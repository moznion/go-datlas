package fixture

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moznion/go-datlas/internal/generator/output"
	"github.com/moznion/go-datlas/internal/generator/testutil"
)

func generateModel(t *testing.T, model string) (string, string, error) {
	t.Helper()
	dir := testutil.WriteTree(t, map[string]string{"model/model.go": model})
	path, err := run(Config{Dir: dir, Patterns: []string{"./model"}, OutDir: "fx"})
	if err != nil {
		return dir, "", err
	}
	testutil.TypeCheck(t, dir, "./...")
	return dir, testutil.ReadFile(t, path), nil
}

func contains(t *testing.T, src string, wants ...string) {
	t.Helper()
	norm := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	for _, want := range wants {
		if !strings.Contains(norm(src), norm(want)) {
			t.Errorf("generated code does not contain %q:\n%s", want, src)
		}
	}
}

func TestOneofEscapes(t *testing.T) {
	_, src, err := generateModel(t, "package model\n\n//datlas:fixture\ntype T struct {\n\tA string `fixture:\"oneof=a\\\\,b|c\\\\|d|e\\\\\\\\f\"`\n}\n")
	if err != nil {
		t.Fatal(err)
	}
	contains(t, src, `[]string{"a,b", "c|d", "e\\f"}[f.g.IntN(3)]`)
}

func TestNestedAndSlices(t *testing.T) {
	model := `package model

//datlas:fixture
type Company struct {
	Name string ` + "`fixture:\"word\"`" + `
}

//datlas:fixture
type User struct {
	Company   Company    ` + "`fixture:\"nested\"`" + `
	Employer  *Company   ` + "`fixture:\"nested\"`" + `
	Past      []Company  ` + "`fixture:\"nested,count=2\"`" + `
	Tags      []string   ` + "`fixture:\"word,count=3\"`" + `
	Scores    []*int     ` + "`fixture:\"int,max=5\"`" + `
	Nicknames []string   ` + "`fixture:\"oneof=a|b,count=0\"`" + `
}
`
	_, src, err := generateModel(t, model)
	if err != nil {
		t.Fatal(err)
	}
	contains(t, src,
		"Company: f.Company(),",
		"Employer: fixturekit.Ptr(f.Company()),",
		"vs := make([]model.Company, 2)",
		"vs[i] = f.g.Word()",
		"vs := make([]*int, 1)",
		"vs[i] = fixturekit.Ptr(int(f.g.Int64Range(1, 5)))",
		"vs := make([]string, 0)",
	)
}

func TestFixtureFeatureErrors(t *testing.T) {
	for model, want := range map[string]string{
		"package model\n\n//datlas:fixture\ntype A struct {\n\tB B `fixture:\"nested\"`\n}\n\n//datlas:fixture\ntype B struct {\n\tA *A `fixture:\"nested\"`\n}\n": "nested fixtures form a cycle: A.B -> B.A -> A",
		"package model\n\n//datlas:fixture\ntype A struct {\n\tS struct{} `fixture:\"nested\"`\n}\n":                                                               `requires a //datlas:fixture type`,
		"package model\n\n//datlas:fixture\ntype A struct {\n\tS string `fixture:\"word,count=2\"`\n}\n":                                                           `"count" is only allowed on slice fields`,
		"package model\n\n//datlas:fixture\ntype A struct {\n\tS []string `fixture:\"word,count=-1\"`\n}\n":                                                        `invalid count "-1"`,
	} {
		_, _, err := generateModel(t, model)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("error %v does not contain %q", err, want)
		}
	}
}

func TestStaleFactoryRemoval(t *testing.T) {
	dir := testutil.WriteTree(t, map[string]string{"model/model.go": "package model\n\n//datlas:fixture\ntype T struct{}\n"})
	path, err := run(Config{Dir: dir, Patterns: []string{"./model"}, OutDir: "fx"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "model/model.go"), []byte("package model\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := Generate(Config{Dir: dir, Patterns: []string{"./model"}, OutDir: "fx"})
	if err != nil {
		t.Fatal(err)
	}
	if r.Src != nil || r.Path != path {
		t.Fatalf("expected removal of %s, got %+v", path, r)
	}
	if _, err := output.Apply(r, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("stale factory was not removed")
	}
}

func TestUnique(t *testing.T) {
	model := "package model\n\n//datlas:fixture\ntype T struct {\n" +
		"\tName  string    `fixture:\"word,unique\"`\n" +
		"\tCode  *int      `fixture:\"int,max=99,unique\"`\n" +
		"\tTags  []string  `fixture:\"oneof=a|b|c,count=2,unique\"`\n" +
		"\tKind  Kind      `fixture:\"word,unique\"`\n" +
		"}\n\ntype Kind string\n"
	_, src, err := generateModel(t, model)
	if err != nil {
		t.Fatal(err)
	}
	contains(t, src,
		`Name: fixturekit.Unique(f.g, "example.com/datlastest/model.T.Name", func() string { return f.g.Word() }),`,
		`Code: fixturekit.Ptr(fixturekit.Unique(f.g, "example.com/datlastest/model.T.Code", func() int { return int(f.g.Int64Range(1, 99)) })),`,
		`vs[i] = fixturekit.Unique(f.g, "example.com/datlastest/model.T.Tags", func() string { return []string{"a", "b", "c"}[f.g.IntN(3)] })`,
		`Kind: fixturekit.Unique(f.g, "example.com/datlastest/model.T.Kind", func() model.Kind { return model.Kind(f.g.Word()) }),`,
	)
}

func TestUniqueErrors(t *testing.T) {
	for tag, want := range map[string]string{
		"email,unique":       `fixture generator "email" is always unique`,
		"seq,unique":         `fixture generator "seq" is always unique`,
		"word,unique,unique": `duplicate fixture tag parameter "unique"`,
		"word,unique=true":   `fixture generator "word" does not accept parameter "unique"`,
	} {
		_, _, err := generateModel(t, "package model\n\n//datlas:fixture\ntype T struct {\n\tA string `fixture:\""+tag+"\"`\n}\n")
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: error %v does not contain %q", tag, err, want)
		}
	}
	_, _, err := generateModel(t, "package model\n\n//datlas:fixture\ntype T struct {\n\tA bool `fixture:\"bool,unique\"`\n}\n")
	if err == nil || !strings.Contains(err.Error(), `fixture generator "bool" cannot be combined with "unique"`) {
		t.Errorf("bool: unexpected error %v", err)
	}
}
