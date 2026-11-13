package declarative

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moznion/go-datlas/internal/generator/output"
	"github.com/moznion/go-datlas/internal/generator/testutil"
)

func mustContain(t *testing.T, src string, wants ...string) {
	t.Helper()
	norm := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	for _, want := range wants {
		if !strings.Contains(norm(src), norm(want)) {
			t.Errorf("generated code does not contain %q:\n%s", want, src)
		}
	}
}

func mustFail(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected an error containing %q", want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error %q does not contain %q", err, want)
	}
}

func TestCrossKindIdentifierConflict(t *testing.T) {
	dir := testutil.WriteTree(t, map[string]string{
		"schema.go":          "package data\n\n//datlas:master local_users\ntype M struct {\n\tA string `master:\"key\"`\n}\n\n//datlas:seed users\ntype S struct {\n\tA string\n}\n",
		"m/local_users.yaml": "[]\n",
		"s/local/users.yaml": "[]\n",
	})
	if _, err := run(MasterData, Config{Dir: dir, DataDir: "m"}); err != nil {
		t.Fatal(err)
	}
	_, err := run(SeedData, Config{Dir: dir, DataDir: "s"})
	mustFail(t, err, "generated identifier LocalUsers conflicts with the master data generated into")
}

func TestIgnoredFiles(t *testing.T) {
	_, err := generate(t, MasterData, map[string]string{
		"schema.go":     roleSchema,
		"roles.yaml":    "[]\n",
		".golangci.yml": "x: 1\n",
		"_draft.yaml":   "x: 1\n",
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestYMLExtension(t *testing.T) {
	path, err := generate(t, MasterData, map[string]string{"schema.go": roleSchema, "roles.yml": "- code: a\n  name: A\n"})
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, testutil.ReadFile(t, path), "compiled from roles.yml")
}

func TestMergeKeyPrecedence(t *testing.T) {
	path, err := generate(t, MasterData, map[string]string{
		"schema.go":  roleSchema,
		"roles.yaml": "- &base {code: a, name: Base, level: 1}\n- <<: [*base, {name: Second, level: 2}]\n  code: b\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	// Explicit keys win over merged ones; among merged mappings the first wins.
	mustContain(t, testutil.ReadFile(t, path), `return Role{ Code: "b", Name: "Base", Level: 1, }`)
}

func TestCopiesAndCompositeKey(t *testing.T) {
	schema := `package data

//datlas:master permissions
type Permission struct {
	Role   string   ` + "`yaml:\"role\" master:\"key\"`" + `
	Action string   ` + "`yaml:\"action\" master:\"key\"`" + `
	Scopes []string ` + "`yaml:\"scopes\"`" + `
}
`
	data := "- {role: admin, action: read, scopes: [a]}\n- {role: admin, action: write, scopes: [a, b]}\n"
	path, err := generate(t, MasterData, map[string]string{"schema.go": schema, "permissions.yaml": data})
	if err != nil {
		t.Fatal(err)
	}
	testutil.TypeCheck(t, filepath.Dir(path), ".")
	mustContain(t, testutil.ReadFile(t, path),
		"func Permissions() []Permission {",
		"func PermissionByRoleAndAction(role string, action string) (Permission, bool) {",
		`case role == "admin" && action == "write": return datlasMasterPermissions1(), true`,
	)

	_, err = generate(t, MasterData, map[string]string{"schema.go": schema, "permissions.yaml": data + "- {role: admin, action: read, scopes: []}\n"})
	mustFail(t, err, `duplicate logical key Role, Action = "admin", "read"`)
}

const enumSchema = `package data

//datlas:enum
type Tier string

const (
	TierFree Tier = "free"
	TierPro  Tier = "pro"
)

// Level is not an enum even though it has constants.
type Level int

const MaxLevel Level = 10

//datlas:master plans
type Plan struct {
	Code  string ` + "`yaml:\"code\" master:\"key\"`" + `
	Tier  Tier   ` + "`yaml:\"tier\"`" + `
	Tiers []Tier ` + "`yaml:\"tiers,omitempty\"`" + `
	Level Level  ` + "`yaml:\"level,omitempty\"`" + `
}
`

func TestEnum(t *testing.T) {
	if _, err := generate(t, MasterData, map[string]string{"schema.go": enumSchema, "plans.yaml": "- {code: a, tier: pro, tiers: [free], level: 99}\n"}); err != nil {
		t.Fatal(err)
	}
	_, err := generate(t, MasterData, map[string]string{"schema.go": enumSchema, "plans.yaml": "- {code: a, tier: gold}\n"})
	mustFail(t, err, `plans.yaml:1:19: "gold" is not a valid data.Tier (allowed: TierFree, TierPro)`)
	_, err = generate(t, MasterData, map[string]string{"schema.go": enumSchema, "plans.yaml": "- {code: a, tier: pro, tiers: [free, x]}\n"})
	mustFail(t, err, `"x" is not a valid data.Tier`)
}

func TestEnumFromAnotherPackage(t *testing.T) {
	dir := testutil.WriteTree(t, map[string]string{
		"model/model.go":  "package model\n\n//datlas:enum\ntype Color int\n\nconst (\n\tRed Color = iota + 1\n\tBlue\n)\n",
		"data/schema.go":  "package data\n\nimport \"" + testutil.Module + "/model\"\n\n//datlas:master items\ntype Item struct {\n\tID    string      `master:\"key\"`\n\tColor model.Color\n}\n",
		"data/items.yaml": "- {id: a, color: 2}\n- {id: b, color: 3}\n",
	})
	_, err := run(MasterData, Config{Dir: dir, Package: "./data"})
	mustFail(t, err, "items.yaml:2:18: 3 is not a valid model.Color (allowed: Blue, Red)")
}

func TestEnumDirectiveErrors(t *testing.T) {
	_, err := generate(t, MasterData, map[string]string{
		"schema.go":  "package data\n\n//datlas:enum\ntype E struct{}\n\n" + strings.TrimPrefix(roleSchema, "package data\n"),
		"roles.yaml": "[]\n",
	})
	mustFail(t, err, "//datlas:enum can only be used on defined string or integer types")

	_, err = generate(t, MasterData, map[string]string{
		"schema.go":  "package data\n\n//datlas:enum\ntype E string\n\n//datlas:master roles\ntype Role struct {\n\tCode string `master:\"key\"`\n\tE    E\n}\n",
		"roles.yaml": "- {code: a, e: x}\n",
	})
	mustFail(t, err, "enum E declares no constants")
}

func TestNonzero(t *testing.T) {
	schema := `package data

import "time"

//datlas:master roles
type Role struct {
	Code  string            ` + "`yaml:\"code\" master:\"key\"`" + `
	Name  string            ` + "`yaml:\"name\" datlas:\"nonzero\"`" + `
	Tags  []string          ` + "`yaml:\"tags,omitempty\" datlas:\"nonzero\"`" + `
	At    time.Time         ` + "`yaml:\"at,omitempty\" datlas:\"nonzero\"`" + `
	Note  *string           ` + "`yaml:\"note\" datlas:\"nonzero\"`" + `
}
`
	ok := "- {code: a, name: A, tags: [x], at: 2024-01-01, note: n}\n"
	if _, err := generate(t, MasterData, map[string]string{"schema.go": schema, "roles.yaml": ok}); err != nil {
		t.Fatal(err)
	}
	for data, want := range map[string]string{
		"- {code: a, name: '', tags: [x], at: 2024-01-01, note: n}\n":   `field "name" must not be empty or zero`,
		"- {code: a, name: A, tags: [], at: 2024-01-01, note: n}\n":     `field "tags" must not be empty or zero`,
		"- {code: a, name: A, tags: [x], note: n}\n":                    `missing required field "at"`,
		"- {code: a, name: A, tags: [x], at: 0001-01-01, note: n}\n":    `field "at" must not be empty or zero`,
		"- {code: a, name: A, tags: [x], at: 2024-01-01, note: null}\n": `field "note" must not be empty or zero`,
		"- {code: '', name: A, tags: [x], at: 2024-01-01, note: n}\n":   `field "code" must not be empty or zero`,
	} {
		_, err := generate(t, MasterData, map[string]string{"schema.go": schema, "roles.yaml": data})
		mustFail(t, err, want)
	}

	_, err := generate(t, MasterData, map[string]string{
		"schema.go":  "package data\n\n//datlas:master roles\ntype Role struct {\n\tCode string `master:\"key\"`\n\tS    struct{ X int } `datlas:\"nonzero\"`\n}\n",
		"roles.yaml": "[]\n",
	})
	mustFail(t, err, "nonzero is not supported for type struct{X int}")
	_, err = generate(t, MasterData, map[string]string{
		"schema.go":  "package data\n\n//datlas:master roles\ntype Role struct {\n\tCode string `master:\"key\" datlas:\"nonempty\"`\n}\n",
		"roles.yaml": "[]\n",
	})
	mustFail(t, err, `unknown datlas tag option "nonempty"`)
}

const moneySchema = `package data

import (
	"errors"
	"strings"
)

// Money is a decimal amount decoded from text.
type Money struct{ text string }

func (m *Money) UnmarshalText(b []byte) error {
	if !strings.Contains(string(b), ".") {
		return errors.New("missing decimal point")
	}
	m.text = string(b)
	return nil
}

func (m Money) String() string { return m.text }

//datlas:master prices
type Price struct {
	Code   string ` + "`yaml:\"code\" master:\"key\"`" + `
	Amount Money  ` + "`yaml:\"amount\"`" + `
	Max    *Money ` + "`yaml:\"max\"`" + `
	Raw    []byte ` + "`yaml:\"raw\"`" + `
}
`

func TestTextUnmarshaler(t *testing.T) {
	path, err := generate(t, MasterData, map[string]string{
		"schema.go":   moneySchema,
		"prices.yaml": "- {code: a, amount: 12.50, max: '99.9', raw: hello}\n",
		// Uses a generated identifier, so the package only compiles with the new generated file.
		"use.go": "package data\n\nvar _ = Prices\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	testutil.TypeCheck(t, filepath.Dir(path), ".")
	mustContain(t, testutil.ReadFile(t, path),
		`Amount: datlasMasterText[Money]("12.50")`,
		`Max: datlasMasterPtr[Money](datlasMasterText[Money]("99.9"))`,
		`Raw: []byte("hello")`,
	)
	if matches, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".datlas-check-*")); len(matches) > 0 {
		t.Fatalf("temporary files were left behind: %v", matches)
	}

	_, err = generate(t, MasterData, map[string]string{
		"schema.go":   moneySchema,
		"prices.yaml": "- {code: a, amount: 12.50, max: 100, raw: x}\n",
	})
	mustFail(t, err, `prices.yaml:1:33: invalid data.Money "100": missing decimal point`)
}

func TestReferences(t *testing.T) {
	schema := `package data

//datlas:master roles
type Role struct {
	Code   string   ` + "`yaml:\"code\" master:\"key\"`" + `
	Parent *string  ` + "`yaml:\"parent\" master:\"ref=roles\"`" + `
}

//datlas:master grants
type Grant struct {
	ID    int      ` + "`yaml:\"id\" master:\"key\"`" + `
	Roles []string ` + "`yaml:\"roles\" master:\"ref=roles\"`" + `
}
`
	roles := "- {code: admin}\n- {code: member, parent: admin}\n"
	if _, err := generate(t, MasterData, map[string]string{"schema.go": schema, "roles.yaml": roles, "grants.yaml": "- {id: 1, roles: [admin, member]}\n"}); err != nil {
		t.Fatal(err)
	}
	_, err := generate(t, MasterData, map[string]string{"schema.go": schema, "roles.yaml": roles + "- {code: guest, parent: nobody}\n", "grants.yaml": "[]\n"})
	mustFail(t, err, `roles.yaml:3:25: Parent refers to "nobody", which does not exist in dataset "roles"`)
	_, err = generate(t, MasterData, map[string]string{"schema.go": schema, "roles.yaml": roles, "grants.yaml": "- {id: 1, roles: [admin, x]}\n"})
	mustFail(t, err, `Roles refers to "x"`)

	for field, want := range map[string]string{
		"X string `master:\"ref=nothing\"`": `reference to unknown master data dataset "nothing"`,
		"X int `master:\"ref=roles\"`":      "reference of integer type cannot refer to the string key",
		"X string `master:\"ref=a.b.c/\"`":  "invalid reference",
		"X []int `master:\"ref=grants\"`":   "",
	} {
		src := strings.Replace(schema, "Parent *string", field+"\n\tParent *string", 1)
		_, err := generate(t, MasterData, map[string]string{"schema.go": src, "roles.yaml": "[]\n", "grants.yaml": "[]\n"})
		if want == "" {
			if err != nil {
				t.Errorf("%s: %v", field, err)
			}
			continue
		}
		mustFail(t, err, want)
	}
}

func TestSeedReferences(t *testing.T) {
	master := "package masterdata\n\n//datlas:master roles\ntype Role struct {\n\tCode string `master:\"key\"`\n}\n"
	seed := "package seeddata\n\n//datlas:seed companies\ntype Company struct {\n\tKey string `seed:\"key\"`\n}\n\n" +
		"//datlas:seed users\ntype User struct {\n\tEmail   string `seed:\"key\"`\n\tCompany string `seed:\"ref=companies\"`\n\tRole    string `seed:\"ref=" + testutil.Module + "/masterdata.roles\"`\n}\n"
	files := map[string]string{
		"masterdata/schema.go":            master,
		"masterdata/roles.yaml":           "- {code: admin}\n- {code: member}\n",
		"seeddata/schema.go":              seed,
		"seeddata/local/companies.yaml":   "- {key: acme}\n",
		"seeddata/local/users.yaml":       "- {email: a, company: acme, role: admin}\n",
		"seeddata/demo/users.yaml":        "- {email: a, company: acme, role: member}\n",
		"seeddata/demo/.hidden/users.yml": "not yaml at all: [",
	}
	dir := testutil.WriteTree(t, files)
	_, err := run(SeedData, Config{Dir: dir, Package: "./seeddata"})
	mustFail(t, err, "has no generated master data; run `datlas master` for it first")

	if _, err := run(MasterData, Config{Dir: dir, Package: "./masterdata"}); err != nil {
		t.Fatal(err)
	}
	// "acme" exists only in the local environment.
	_, err = run(SeedData, Config{Dir: dir, Package: "./seeddata"})
	mustFail(t, err, `demo/users.yaml:1:23: Company refers to "acme", which does not exist in dataset "companies" of the "demo" environment`)

	if err := os.WriteFile(filepath.Join(dir, "seeddata/demo/users.yaml"), []byte("- {email: a, company: acme, role: owner}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "seeddata/demo/companies.yaml"), []byte("- {key: acme}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = run(SeedData, Config{Dir: dir, Package: "./seeddata"})
	mustFail(t, err, `Role refers to "owner", which does not exist in dataset "`+testutil.Module+`/masterdata.roles"`)

	if err := os.WriteFile(filepath.Join(dir, "seeddata/demo/users.yaml"), []byte("- {email: a, company: acme, role: member}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := run(SeedData, Config{Dir: dir, Package: "./seeddata"}); err != nil {
		t.Fatal(err)
	}
	testutil.TypeCheck(t, dir, "./...")
}

func TestSeedEnvironments(t *testing.T) {
	dir := testutil.WriteTree(t, map[string]string{
		"schema.go":         userSeedSchema,
		"local/users.yaml":  "- email: a@example.test\n",
		"local/events.yaml": "- title: Launch\n",
		"demo/users.yaml":   "[]\n",
	})
	path, err := run(SeedData, Config{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	testutil.TypeCheck(t, dir, ".")
	mustContain(t, testutil.ReadFile(t, path),
		`func SeedEnvironments() []string { return []string{"demo", "local"} }`,
		"type SeedSet struct {",
		"Users []UserSeed",
		"Events []EventSeed",
		"func SeedFor(env string) (SeedSet, bool) {",
		`case "demo": return SeedSet{ Events: DemoEvents(), Users: DemoUsers(), }, true`,
		// The demo environment has no events.yaml, but gets an empty accessor for symmetry.
		`func DemoEvents() []EventSeed { return []EventSeed{} }`,
	)
}

func TestStaleFileRemoval(t *testing.T) {
	dir := testutil.WriteTree(t, map[string]string{"schema.go": roleSchema, "roles.yaml": "[]\n"})
	path, err := run(MasterData, Config{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "schema.go"), []byte("package data\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// YAML left without a schema is an error, even when the generated file becomes stale.
	_, err = Generate(MasterData, Config{Dir: dir})
	mustFail(t, err, `no //datlas:master type declares dataset "roles"`)
	if err := os.Remove(filepath.Join(dir, "roles.yaml")); err != nil {
		t.Fatal(err)
	}
	r, err := Generate(MasterData, Config{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if r.Src != nil || r.Path != path {
		t.Fatalf("expected removal of %s, got %+v", path, r)
	}
	if _, err := output.Apply(r, true); err == nil {
		t.Fatal("check mode must report the stale file")
	}
	if _, err := output.Apply(r, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("stale file was not removed")
	}
	_, err = Generate(MasterData, Config{Dir: dir})
	mustFail(t, err, "no //datlas:master types found")
}

func TestMissingDataDir(t *testing.T) {
	dir := testutil.WriteTree(t, map[string]string{"schema.go": roleSchema})
	_, err := run(MasterData, Config{Dir: dir, DataDir: "nope"})
	mustFail(t, err, "data directory")
}
