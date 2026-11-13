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

func TestCustomGenerator(t *testing.T) {
	dir := testutil.WriteTree(t, map[string]string{
		"fake/fake.go": `package fake

import (
	"time"

	"github.com/moznion/go-datlas/fixturekit"
)

type SKU string

func NewSKU(g *fixturekit.Generator) SKU { return SKU("SKU-" + g.AlphaNum(8)) }

func Code(g *fixturekit.Generator) string { return g.Alpha(4) }

func Labels(g *fixturekit.Generator) map[string]string { return map[string]string{"k": g.Word()} }

func TTL(g *fixturekit.Generator) time.Duration { return time.Duration(g.Int64Range(1, 60)) * time.Second }
`,
		"model/model.go": `package model

import (
	"database/sql"
	"time"

	"example.com/datlastest/fake"
)

//datlas:fixture
type Item struct {
	SKU    fake.SKU          ` + "`fixture:\"gen=example.com/datlastest/fake.NewSKU\"`" + `
	Codes  []string          ` + "`fixture:\"gen=example.com/datlastest/fake.Code,count=2\"`" + `
	Alt    *string           ` + "`fixture:\"gen=example.com/datlastest/fake.Code\"`" + `
	Labels map[string]string ` + "`fixture:\"gen=example.com/datlastest/fake.Labels\"`" + `
	TTL    time.Duration     ` + "`fixture:\"gen=example.com/datlastest/fake.TTL\"`" + `
	Note   sql.NullString    ` + "`fixture:\"gen=example.com/datlastest/fake.Code,wrap=nullString\"`" + `
	Local  string            ` + "`fixture:\"gen=local\"`" + `
}
`,
		"fx/helpers.go": `package fx

import (
	"database/sql"

	"github.com/moznion/go-datlas/fixturekit"
)

func nullString(s string) sql.NullString { return sql.NullString{String: s, Valid: true} }

func local(g *fixturekit.Generator) string { return g.Word() }
`,
	})
	path, err := run(Config{Dir: dir, Patterns: []string{"./model", "./fake"}, OutDir: "fx"})
	if err != nil {
		t.Fatal(err)
	}
	testutil.TypeCheck(t, dir, "./...")
	contains(t, testutil.ReadFile(t, path),
		`SKU: fake.NewSKU(f.g),`,
		`vs[i] = fake.Code(f.g)`,
		`Alt: fixturekit.Ptr(fake.Code(f.g)),`,
		`Labels: fake.Labels(f.g),`,
		`TTL: fake.TTL(f.g),`,
		`Note: nullString(fake.Code(f.g)),`,
		`Local: local(f.g),`,
	)
}

func TestCustomGeneratorErrors(t *testing.T) {
	model := func(field string) string {
		return "package model\n\nimport \"github.com/moznion/go-datlas/fixturekit\"\n\n" +
			"func Bad(n int) string { return \"\" }\n\n" +
			"func Num(g *fixturekit.Generator) int { return 1 }\n\n" +
			"func Gen[T any](g *fixturekit.Generator) T { var v T; return v }\n\n" +
			"//datlas:fixture\ntype T struct {\n\t" + field + "\n}\n"
	}
	for field, want := range map[string]string{
		"A string `fixture:\"gen\"`":                                            `fixture generator "gen" requires a function`,
		"A string `fixture:\"gen=a|b\"`":                                        `fixture generator "gen" takes one function`,
		"A string `fixture:\"gen=Missing\"`":                                    "gen function Missing is not declared in the output package",
		"A string `fixture:\"gen=example.com/datlastest/model.Bad\"`":           "gen function example.com/datlastest/model.Bad must be func(*fixturekit.Generator) T",
		"A string `fixture:\"gen=example.com/datlastest/model.Num\"`":           "gen function example.com/datlastest/model.Num returns int, which cannot be assigned to string",
		"A []string `fixture:\"gen=example.com/datlastest/model.Num,count=2\"`": "returns int, which cannot be assigned to string",
		"A string `fixture:\"gen=example.com/datlastest/model.Gen\"`":           "must not be generic",
	} {
		_, _, err := generateModel(t, model(field))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: error %v does not contain %q", field, err, want)
		}
	}
}

func TestCustomGeneratorInPolicyFile(t *testing.T) {
	dir := testutil.WriteTree(t, map[string]string{
		"gen/gen.go": "package gen\n\ntype Row struct {\n\tCode string\n}\n",
		"fake/fake.go": "package fake\n\nimport \"github.com/moznion/go-datlas/fixturekit\"\n\n" +
			"func Code(g *fixturekit.Generator) string { return g.Alpha(4) }\n",
		"fx/fixture.yaml": "types:\n  - type: example.com/datlastest/gen.Row\n    fields:\n      Code: gen=example.com/datlastest/fake.Code\n",
	})
	// The package of the gen function is loaded because the policy file refers to it.
	path, err := run(Config{Dir: dir, OutDir: "fx", PolicyFile: "fx/fixture.yaml"})
	if err != nil {
		t.Fatal(err)
	}
	testutil.TypeCheck(t, dir, "./...")
	contains(t, testutil.ReadFile(t, path), `Code: fake.Code(f.g),`)
}

func TestCustomGeneratorUnique(t *testing.T) {
	model := "package model\n\nimport \"github.com/moznion/go-datlas/fixturekit\"\n\n" +
		"func Code(g *fixturekit.Generator) string { return g.AlphaNum(8) }\n\n" +
		"func Labels(g *fixturekit.Generator) map[string]string { return nil }\n\n" +
		"//datlas:fixture\ntype T struct {\n\tA string `fixture:\"gen=Code,unique\"`\n}\n"
	model = strings.Replace(model, "gen=Code", "gen=example.com/datlastest/model.Code", 1)
	_, src, err := generateModel(t, model)
	if err != nil {
		t.Fatal(err)
	}
	contains(t, src, `A: fixturekit.Unique(f.g, "example.com/datlastest/model.T.A", func() string { return model.Code(f.g) }),`)

	bad := strings.Replace(model, "A string `fixture:\"gen=example.com/datlastest/model.Code,unique\"`",
		"A map[string]string `fixture:\"gen=example.com/datlastest/model.Labels,unique\"`", 1)
	_, _, err = generateModel(t, bad)
	if err == nil || !strings.Contains(err.Error(), `"unique" requires a comparable type, not map[string]string`) {
		t.Fatalf("unexpected error %v", err)
	}
}
