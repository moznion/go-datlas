package fixture

import (
	"strings"
	"testing"

	"github.com/moznion/go-datlas/internal/generator/testutil"
)

func TestOutputPackageCollisions(t *testing.T) {
	dir := testutil.WriteTree(t, map[string]string{
		"model.go": "package model\n\ntype Option struct{}\n\n//datlas:fixture\ntype User struct {\n\tName string `fixture:\"name\"`\n}\n",
	})
	_, err := run(Config{Dir: dir, Patterns: []string{"."}, OutDir: "."})
	if err == nil || !strings.Contains(err.Error(), "Option is already declared in the output package") {
		t.Fatalf("unexpected error: %v", err)
	}

	// Package-level identifiers named like an import must not be shadowed.
	dir = testutil.WriteTree(t, map[string]string{
		"model/model.go": "package model\n\nimport \"time\"\n\n//datlas:fixture\ntype User struct {\n\tAt time.Time `fixture:\"now\"`\n}\n",
		"fx/helpers.go":  "package fx\n\nvar time = 1\nvar model = 2\n\nvar _, _ = time, model\n",
	})
	if _, err := run(Config{Dir: dir, Patterns: []string{"./model"}, OutDir: "fx"}); err != nil {
		t.Fatal(err)
	}
	testutil.TypeCheck(t, dir, "./...")
}

func TestUnexportedFieldTypes(t *testing.T) {
	for field, want := range map[string]string{
		"Status status `fixture:\"oneof=a|b\"`": "type status is unexported",
		"Codes []*code `fixture:\"word\"`":      "type code is unexported",
	} {
		_, _, err := generateModel(t, "package model\n\ntype status string\ntype code string\n\n//datlas:fixture\ntype User struct {\n\t"+field+"\n}\n")
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: error %v does not contain %q", field, err, want)
		}
	}
}

func TestNameOptions(t *testing.T) {
	dir := testutil.WriteTree(t, map[string]string{
		"a/a.go": "package a\n\n//datlas:fixture name=AdminUser\ntype User struct{}\n",
		"b/b.go": "package b\n\n//datlas:fixture name=Person plural=Crowd\ntype User struct{}\n\n//datlas:fixture\ntype Status struct{}\n",
	})
	path, err := run(Config{Dir: dir, Patterns: []string{"./a", "./b"}, OutDir: "fx"})
	if err != nil {
		t.Fatal(err)
	}
	testutil.TypeCheck(t, dir, "./...")
	contains(t, testutil.ReadFile(t, path),
		"func (f *Factory) AdminUser(opts ...func(*a.User)) a.User",
		"func (f *Factory) AdminUsers(n int",
		"func (f *Factory) Person(opts ...func(*b.User)) b.User",
		"func (f *Factory) Crowd(n int",
		"func (f *Factory) Statuses(n int",
	)
	for directive, want := range map[string]string{
		"//datlas:fixture nope=1":  `unknown option "nope=1"`,
		"//datlas:fixture name=1x": `"1x" is not a valid Go identifier`,
	} {
		_, _, err := generateModel(t, "package model\n\n"+directive+"\ntype T struct{}\n")
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: error %v does not contain %q", directive, err, want)
		}
	}
}

const enumModel = `package model

//datlas:enum
type Status string

const (
	Active Status = "active"
	Gone   Status = "gone"
)

//datlas:enum
type Level int

const (
	Low  Level = 1
	High Level = 9
)
`

func TestEnumGenerator(t *testing.T) {
	_, src, err := generateModel(t, enumModel+"\n//datlas:fixture\ntype User struct {\n\tStatus Status `fixture:\"enum\"`\n\tLevel *Level `fixture:\"enum\"`\n\tPick Status `fixture:\"oneof=gone\"`\n}\n")
	if err != nil {
		t.Fatal(err)
	}
	contains(t, src,
		`Status: []model.Status{"active", "gone"}[f.g.IntN(2)]`,
		`Level: fixturekit.Ptr([]model.Level{9, 1}[f.g.IntN(2)])`,
	)
	for field, want := range map[string]string{
		"S Status `fixture:\"oneof=actve|gone\"`": `oneof candidate "actve" is not a valid`,
		"S string `fixture:\"enum\"`":             `requires a //datlas:enum type`,
	} {
		_, _, err := generateModel(t, enumModel+"\n//datlas:fixture\ntype User struct {\n\t"+field+"\n}\n")
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: error %v does not contain %q", field, err, want)
		}
	}
}

func TestEmbeddedStructs(t *testing.T) {
	model := `package model

import "time"

type Timestamps struct {
	CreatedAt time.Time ` + "`fixture:\"now\"`" + `
	UpdatedAt time.Time ` + "`fixture:\"now\"`" + `
}

type Plain struct{ X int }

//datlas:fixture
type Owner struct {
	Name string ` + "`fixture:\"name\"`" + `
}

//datlas:fixture
type User struct {
	Timestamps
	Plain
	Owner ` + "`fixture:\"nested\"`" + `
	Email string ` + "`fixture:\"email\"`" + `
}
`
	_, src, err := generateModel(t, model)
	if err != nil {
		t.Fatal(err)
	}
	contains(t, src,
		"Timestamps: model.Timestamps{ CreatedAt: f.g.Now(), UpdatedAt: f.g.Now(), },",
		"Owner: f.Owner(),",
	)
	if strings.Contains(src, "Plain:") {
		t.Errorf("embedded struct without generated fields was initialised:\n%s", src)
	}
	_, _, err = generateModel(t, "package model\n\ntype Base struct{ ID int }\n\n//datlas:fixture\ntype User struct {\n\tBase `fixture:\"int\"`\n}\n")
	if err == nil || !strings.Contains(err.Error(), `embedded fields only support the "nested" generator`) {
		t.Fatalf("unexpected error: %v", err)
	}
}
