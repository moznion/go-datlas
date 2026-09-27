package fixture

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moznion/go-datlas/internal/generator/testutil"
)

func TestSkeleton(t *testing.T) {
	dir := testutil.WriteTree(t, map[string]string{
		"gen/gen.go": "package gen\n\ntype User struct {\n\tName string\n}\n",
		"fx/user.go": "package fx\n\nimport \"" + testutil.Module + "/gen\"\n\n" +
			"func (f *Factory) User() gen.User {\n\treturn gen.User{Name: f.Faker().Name()}\n}\n",
	})
	path, err := run(Config{Dir: dir, OutDir: "fx"})
	if err != nil {
		t.Fatal(err)
	}
	testutil.TypeCheck(t, dir, "./...")
	src := testutil.ReadFile(t, path)
	contains(t, src, "type Factory struct", "func (f *Factory) Faker() *fixturekit.Generator")
	if strings.Contains(src, "func (f *Factory) User") {
		t.Fatalf("skeleton declares a factory method:\n%s", src)
	}

	// The output package does not need to exist yet.
	dir = testutil.WriteTree(t, map[string]string{})
	if _, err := run(Config{Dir: dir, OutDir: "fixture"}); err != nil {
		t.Fatal(err)
	}
	testutil.TypeCheck(t, dir, "./...")
}

func TestHandWrittenConflicts(t *testing.T) {
	model := "package model\n\n//datlas:fixture\ntype User struct {\n\tName string `fixture:\"name\"`\n}\n"
	for method, want := range map[string]string{
		"User":  "factory method User for example.com/datlastest/model.User conflicts with the hand-written method Factory.User at ",
		"Users": "factory method Users for example.com/datlastest/model.User conflicts with the hand-written method Factory.Users at ",
		"Seed":  "Factory.Seed is declared by hand, but the generated factory declares it too",
	} {
		dir := testutil.WriteTree(t, map[string]string{
			"model/model.go": model,
			"fx/hand.go":     "package fx\n\nfunc (f *Factory) " + method + "() {}\n",
			// Methods of an external test package do not belong to Factory.
			"fx/x_test.go": "package fx_test\n\ntype Factory struct{}\n\nfunc (Factory) Other() {}\n",
		})
		_, err := run(Config{Dir: dir, Patterns: []string{"./model"}, OutDir: "fx"})
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: error %v does not contain %q", method, err, want)
		}
	}

	// Hand-written methods with other names coexist with generated ones.
	dir := testutil.WriteTree(t, map[string]string{
		"model/model.go": model,
		"fx/hand.go": "package fx\n\nimport \"" + testutil.Module + "/model\"\n\n" +
			"func (f *Factory) Admin() model.User {\n\treturn f.User(func(u *model.User) { u.Name = \"admin\" })\n}\n",
	})
	if _, err := run(Config{Dir: dir, Patterns: []string{"./model"}, OutDir: "fx"}); err != nil {
		t.Fatal(err)
	}
	testutil.TypeCheck(t, dir, "./...")
}

const genSrc = `package gen

import (
	"database/sql"

	"example.com/datlastest/optional"
	"example.com/datlastest/model"
)

// InsertUserParams stands in for a struct generated from DDL.
type InsertUserParams struct {
	ID       [16]byte
	Email    string
	Age      int32
	Bio      sql.NullString
	Nick     sql.Null[string]
	Website  optional.Option[string]
	Role     string
	Company  model.Company
	Note     string
	internal string
}

//datlas:fixture
type NotScanned struct {
	Name string ` + "`fixture:\"name\"`" + `
}

var _ = InsertUserParams{}.internal
`

const optSrc = `package optional

type Option[T any] []T

func Some[T any](v T) Option[T] { return Option[T]{v} }
`

const policySrc = `types:
  - type: example.com/datlastest/gen.InsertUserParams
    name: User
    fields:
      ID: uuid
      Email: email
      Age: int,min=18,max=99
      Bio: sentence,wrap=nullString
      Nick: username,wrap=null
      Website: url,wrap=example.com/datlastest/optional.Some
      Role: oneof=admin|member
      Company: nested
      Note: "-"
  - type: example.com/datlastest/gen.InsertUserParams
    name: Admin
    plural: Admins
    fields:
      ID: uuid
      Email: email
      Age: "-"
      Bio: "-"
      Nick: "-"
      Website: "-"
      Role: oneof=admin
      Company: "-"
      Note: "-"
`

const wrapSrc = `package fx

import "database/sql"

func nullString(s string) sql.NullString { return sql.NullString{String: s, Valid: true} }

func null[T any](v T) sql.Null[T] { return sql.Null[T]{V: v, Valid: true} }
`

func externalTree(t *testing.T, overrides map[string]string) string {
	t.Helper()
	files := map[string]string{
		"model/model.go":       "package model\n\n//datlas:fixture\ntype Company struct {\n\tName string `fixture:\"word\"`\n}\n",
		"gen/gen.go":           genSrc,
		"optional/optional.go": optSrc,
		"fx/wrap.go":           wrapSrc,
		"fx/policy.yaml":       policySrc,
	}
	for k, v := range overrides {
		files[k] = v
	}
	return testutil.WriteTree(t, files)
}

func TestPolicyFile(t *testing.T) {
	dir := externalTree(t, nil)
	path, err := run(Config{Dir: dir, Patterns: []string{"./model"}, PolicyFile: "fx/policy.yaml", OutDir: "fx"})
	if err != nil {
		t.Fatal(err)
	}
	testutil.TypeCheck(t, dir, "./...")
	src := testutil.ReadFile(t, path)
	contains(t, src,
		"func (f *Factory) User(opts ...func(*gen.InsertUserParams)) gen.InsertUserParams",
		"func (f *Factory) Users(n int, opts ...func(*gen.InsertUserParams)) []gen.InsertUserParams",
		"func (f *Factory) Admin(opts ...func(*gen.InsertUserParams)) gen.InsertUserParams",
		"func (f *Factory) Admins(n int",
		"Age: int32(f.g.Int64Range(18, 99)),",
		"Bio: nullString(f.g.Sentence()),",
		"Nick: null[string](f.g.Username()),",
		"Website: optional.Some[string](f.g.URL()),",
		"Company: f.Company(),",
	)
	for _, unwanted := range []string{"Note:", "internal", "NotScanned"} {
		if strings.Contains(src, unwanted) {
			t.Errorf("generated code contains %s:\n%s", unwanted, src)
		}
	}

	// Without PACKAGES, only the policy file is used; the nested type must then come from it too.
	policy := strings.Replace(policySrc, "      Company: nested\n", "      Company: \"-\"\n", 1)
	dir = externalTree(t, map[string]string{"fx/policy.yaml": policy})
	if _, err := run(Config{Dir: dir, PolicyFile: "fx/policy.yaml", OutDir: "fx"}); err != nil {
		t.Fatal(err)
	}
	testutil.TypeCheck(t, dir, "./...")
}

func TestPolicyFileErrors(t *testing.T) {
	entry := func(fields string) string {
		return "types:\n  - type: example.com/datlastest/gen.T\n    fields:\n" + fields
	}
	gen := "package gen\n\nimport \"database/sql\"\n\ntype T struct {\n\tA string\n\tB sql.NullString\n\tc string\n}\n\ntype NotStruct int\n\ntype G[X any] struct{ V X }\n\nfunc bad(a, b string) string { return a }\n\nvar _ = T{}.c\n"
	tests := []struct {
		name, policy, want string
	}{
		{"missing field", entry("      A: word\n"), "T.B has no policy; give it one, or \"-\" to leave it at its zero value"},
		{"unknown field", entry("      A: word\n      B: \"-\"\n      Z: word\n"), "T has no field Z"},
		{"unexported field", entry("      A: word\n      B: \"-\"\n      c: word\n"), "T.c: unexported field cannot be set"},
		{"bad generator", entry("      A: nope\n      B: \"-\"\n"), `unknown fixture generator "nope"`},
		{"wrong type", entry("      A: word\n      B: word\n"), "cannot produce a value of type database/sql.NullString"},
		{"unknown type", "types:\n  - type: example.com/datlastest/gen.Nope\n", "type Nope is not declared in package example.com/datlastest/gen"},
		{"not a struct", "types:\n  - type: example.com/datlastest/gen.NotStruct\n", "gen.NotStruct is not a struct type"},
		{"generic", "types:\n  - type: example.com/datlastest/gen.G\n", "gen.G is generic"},
		{"bad type reference", "types:\n  - type: T\n", "must be an import path followed by a type name"},
		{"unknown key", "types:\n  - type: example.com/datlastest/gen.T\n    nme: X\n", `unknown key "nme"`},
		{"unknown top-level key", "type: x\n", `unknown key "type" (available: types)`},
		{"duplicate key", entry("      A: word\n      A: word\n      B: \"-\"\n"), `mapping key "A" already defined`},
		{"non-string policy", entry("      A: 1\n      B: \"-\"\n"), "the policy of A must be a non-empty string, got an integer"},
		{"invalid name", "types:\n  - type: example.com/datlastest/gen.T\n    name: func\n", `name "func" is not a valid Go identifier`},
		{"wrap not found", entry("      A: word\n      B: word,wrap=nope\n"), "wrap function nope is not declared in the output package"},
		{"wrap unexported", entry("      A: word\n      B: word,wrap=example.com/datlastest/gen.bad\n"), "wrap function example.com/datlastest/gen.bad is unexported"},
		{"wrap not assignable", entry("      A: word,wrap=nullString\n      B: \"-\"\n"), "wrap function nullString returns database/sql.NullString, which cannot be assigned to string"},
		{"wrap not inferable", entry("      A: word,wrap=null\n      B: \"-\"\n"), "cannot infer the type arguments of the generic wrap function null from string"},
		{"wrap with count", entry("      A: word,count=2,wrap=nullString\n      B: \"-\"\n"), `parameter "count" cannot be combined with "wrap"`},
		{"name conflict", "types:\n  - type: example.com/datlastest/gen.T\n    name: Company\n    fields:\n      A: word\n      B: \"-\"\n", "rename it with name: in "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := externalTree(t, map[string]string{"gen/gen.go": gen, "fx/policy.yaml": tt.policy})
			_, err := run(Config{Dir: dir, Patterns: []string{"./model"}, PolicyFile: "fx/policy.yaml", OutDir: "fx"})
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error %q does not contain %q", err, tt.want)
			}
		})
	}
}

func TestPolicyFileErrorPositions(t *testing.T) {
	dir := externalTree(t, map[string]string{
		"gen/gen.go":     "package gen\n\ntype T struct {\n\tA string\n}\n",
		"fx/policy.yaml": "types:\n  - type: example.com/datlastest/gen.T\n    fields:\n      A: nope\n",
	})
	_, err := run(Config{Dir: dir, PolicyFile: "fx/policy.yaml", OutDir: "fx"})
	want := filepath.Join(dir, "fx", "policy.yaml") + ":4:7: T.A: unknown fixture generator"
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("error %v does not contain %q", err, want)
	}
}

func TestNestedAmbiguous(t *testing.T) {
	policy := "types:\n  - type: example.com/datlastest/model.Company\n    name: OtherCompany\n    fields:\n      Name: word\n" +
		"  - type: example.com/datlastest/gen.T\n    fields:\n      C: nested\n"
	dir := externalTree(t, map[string]string{
		"gen/gen.go":     "package gen\n\nimport \"example.com/datlastest/model\"\n\ntype T struct {\n\tC model.Company\n}\n",
		"fx/policy.yaml": policy,
	})
	_, err := run(Config{Dir: dir, Patterns: []string{"./model"}, PolicyFile: "fx/policy.yaml", OutDir: "fx"})
	if err == nil || !strings.Contains(err.Error(), `"nested" cannot choose between the 2 factories of example.com/datlastest/model.Company`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestWrapInStructTag(t *testing.T) {
	model := "package model\n\nimport \"example.com/datlastest/optional\"\n\n//datlas:fixture\ntype User struct {\n\tNick optional.Option[string] `fixture:\"username,wrap=example.com/datlastest/optional.Some\"`\n}\n"
	dir := externalTree(t, map[string]string{"model/model.go": model, "gen/gen.go": "package gen\n"})
	if err := os.Remove(filepath.Join(dir, "fx", "policy.yaml")); err != nil {
		t.Fatal(err)
	}
	path, err := run(Config{Dir: dir, Patterns: []string{"./model"}, OutDir: "fx"})
	if err != nil {
		t.Fatal(err)
	}
	testutil.TypeCheck(t, dir, "./...")
	contains(t, testutil.ReadFile(t, path), "Nick: optional.Some[string](f.g.Username()),")
}
