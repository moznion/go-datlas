package declarative

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/moznion/go-datlas/internal/generator/testutil"
)

func TestNestedReferencesAndInlineKeys(t *testing.T) {
	schema := `package data

//datlas:master roles
type Role struct {
	Code string ` + "`yaml:\"code\" master:\"key\"`" + `
}

type Grant struct {
	Role string ` + "`yaml:\"role\" master:\"ref=roles\"`" + `
}

type Base struct {
	Code string ` + "`yaml:\"code\" master:\"key\"`" + `
}

//datlas:master users
type User struct {
	Base   ` + "`yaml:\",inline\"`" + `
	Grants []Grant          ` + "`yaml:\"grants\"`" + `
	ByName map[string]Grant ` + "`yaml:\"by_name,omitempty\"`" + `
}
`
	roles := "- {code: admin}\n"
	path, err := generate(t, MasterData, map[string]string{"schema.go": schema, "roles.yaml": roles, "users.yaml": "- {code: u1, grants: [{role: admin}], by_name: {x: {role: admin}}}\n"})
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, testutil.ReadFile(t, path), "func UserByCode(key string) (User, bool) {", `case "u1":`)

	_, err = generate(t, MasterData, map[string]string{"schema.go": schema, "roles.yaml": roles, "users.yaml": "- {code: u1, grants: [{role: no-such-role}]}\n"})
	mustFail(t, err, `Role refers to "no-such-role"`)
	_, err = generate(t, MasterData, map[string]string{"schema.go": schema, "roles.yaml": roles, "users.yaml": "- {code: u1, grants: [], by_name: {x: {role: nope}}}\n"})
	mustFail(t, err, `Role refers to "nope"`)
	_, err = generate(t, MasterData, map[string]string{"schema.go": schema, "roles.yaml": roles, "users.yaml": "- {code: u1, grants: []}\n- {code: u1, grants: []}\n"})
	mustFail(t, err, `duplicate logical key Code = "u1"`)

	nestedKey := strings.Replace(schema, "Role string `yaml:\"role\" master:\"ref=roles\"`", "Role string `yaml:\"role\" master:\"key\"`", 1)
	_, err = generate(t, MasterData, map[string]string{"schema.go": nestedKey, "roles.yaml": roles, "users.yaml": "[]\n"})
	mustFail(t, err, "a logical key must be a field of User or of a struct inlined into it")
}

func TestSelfReferencingSchema(t *testing.T) {
	schema := `package data

//datlas:master nodes
type Node struct {
	Parent *Node  ` + "`yaml:\"parent\"`" + `
	Code   string ` + "`yaml:\"code\" master:\"key\"`" + `
}
`
	path, err := generate(t, MasterData, map[string]string{"schema.go": schema, "nodes.yaml": "- {code: a, parent: {code: b}}\n- {code: b}\n"})
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, testutil.ReadFile(t, path), `case "a": return datlasMasterNodes0(), true`)
}

func TestUnexportedTextUnmarshaler(t *testing.T) {
	src := strings.ReplaceAll(moneySchema, "Money", "money")
	src = strings.ReplaceAll(src, "Amount money", "Amount money")
	path, err := generate(t, MasterData, map[string]string{"schema.go": src, "prices.yaml": "- {code: a, amount: 1.5, max: null, raw: x}\n"})
	if err != nil {
		t.Fatal(err)
	}
	testutil.TypeCheck(t, filepath.Dir(path), ".")
	_, err = generate(t, MasterData, map[string]string{"schema.go": src, "prices.yaml": "- {code: a, amount: 1, max: null, raw: x}\n"})
	mustFail(t, err, `invalid data.money "1": missing decimal point`)
}

func TestTextUnmarshalerInMainPackage(t *testing.T) {
	src := strings.Replace(moneySchema, "package data", "package main", 1) + "\nfunc main() {}\n"
	_, err := generate(t, MasterData, map[string]string{"schema.go": src, "prices.yaml": "- {code: a, amount: 1.5, max: null, raw: x}\n"})
	mustFail(t, err, "cannot be validated in a main package")
}

func TestEnumWithTextUnmarshaler(t *testing.T) {
	schema := `package data

import "errors"

//datlas:enum
type Tier string

const TierFree Tier = "free"

func (t *Tier) UnmarshalText(b []byte) error { return errors.New("never called") }

//datlas:master plans
type Plan struct {
	Tier Tier ` + "`yaml:\"tier\" master:\"key\" datlas:\"nonzero\"`" + `
}
`
	path, err := generate(t, MasterData, map[string]string{"schema.go": schema, "plans.yaml": "- {tier: free}\n"})
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, testutil.ReadFile(t, path), `Tier: "free",`, "func PlanByTier(key Tier) (Plan, bool)")
	_, err = generate(t, MasterData, map[string]string{"schema.go": schema, "plans.yaml": "- {tier: gold}\n"})
	mustFail(t, err, `"gold" is not a valid data.Tier`)
}

func TestNestedYAMLIsReported(t *testing.T) {
	_, err := generate(t, SeedData, map[string]string{
		"schema.go":               userSeedSchema,
		"local/users.yaml":        "[]\n",
		"local/events.yaml":       "[]\n",
		"local/extra/users.yaml":  "[]\n",
		"stray/deeper/users.yaml": "[]\n",
		"testdata/local/x.yaml":   "[]\n",
		"sub/pkg.go":              "package sub\n",
		"sub/local/users.yaml":    "[]\n",
	})
	mustFail(t, err, "local/extra/users.yaml: dataset files in subdirectories are not loaded")
	mustFail(t, err, "stray/deeper/users.yaml: seed data must be placed directly in an environment directory")
	for _, skipped := range []string{"testdata", "sub/local"} {
		if strings.Contains(err.Error(), skipped) {
			t.Errorf("%s must be skipped: %v", skipped, err)
		}
	}

	_, err = generate(t, MasterData, map[string]string{"schema.go": roleSchema, "roles.yaml": "[]\n", "old/roles.yaml": "[]\n"})
	mustFail(t, err, "old/roles.yaml: dataset files in subdirectories are not loaded")
}

func TestLookupParameterShadowing(t *testing.T) {
	schema := "package data\n\n//datlas:master perms\ntype perm struct {\n\tPerm   string `master:\"key\"`\n\tAction string `master:\"key\"`\n\tFunc   string `master:\"key\"`\n}\n"
	path, err := generate(t, MasterData, map[string]string{"schema.go": schema, "perms.yaml": "- {perm: a, action: b, func: c}\n"})
	if err != nil {
		t.Fatal(err)
	}
	testutil.TypeCheck(t, filepath.Dir(path), ".")
	mustContain(t, testutil.ReadFile(t, path), "func PermByPermAndActionAndFunc(permKey string, action string, funcKey string) (perm, bool)")
}

func TestSingularOption(t *testing.T) {
	schema := "package data\n\n//datlas:master movies\ntype Movie struct {\n\tCode string `master:\"key\"`\n}\n\n//datlas:master people singular=Human\ntype Person struct {\n\tCode string `master:\"key\"`\n}\n"
	path, err := generate(t, MasterData, map[string]string{"schema.go": schema, "movies.yaml": "[]\n", "people.yaml": "[]\n"})
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, testutil.ReadFile(t, path), "func MovieByCode(", "func HumanByCode(")

	_, err = generate(t, MasterData, map[string]string{"schema.go": strings.Replace(schema, "singular=Human", "plural=x", 1), "movies.yaml": "[]\n", "people.yaml": "[]\n"})
	mustFail(t, err, `unknown or repeated option "plural=x"`)
}

func TestAnchorRedefinition(t *testing.T) {
	path, err := generate(t, MasterData, map[string]string{
		"schema.go":  roleSchema,
		"roles.yaml": "- code: a\n  name: &n First\n- code: b\n  name: *n\n- code: c\n  name: &n Second\n- code: d\n  name: *n\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, testutil.ReadFile(t, path), `Code: "b", Name: "First",`, `Code: "d", Name: "Second",`)
}

func TestErrorsAreNotDuplicated(t *testing.T) {
	_, err := generate(t, MasterData, map[string]string{"schema.go": roleSchema, "roles.yaml": "- code: a\n  name: *nope\n"})
	mustFail(t, err, "unknown alias *nope")
	if n := strings.Count(err.Error(), "unknown alias"); n != 1 {
		t.Fatalf("error reported %d times: %v", n, err)
	}
}
