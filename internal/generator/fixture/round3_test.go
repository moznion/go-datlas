package fixture

import (
	"strings"
	"testing"
)

func TestUnexportedEmbeddedStruct(t *testing.T) {
	model := `package model

import "time"

type base struct {
	CreatedAt time.Time ` + "`fixture:\"now\"`" + `
	inner
}

type inner struct {
	Version int ` + "`fixture:\"int,max=9\"`" + `
}

//datlas:fixture
type User struct {
	base
	Name string ` + "`fixture:\"name\"`" + `
}
`
	_, src, err := generateModel(t, model)
	if err != nil {
		t.Fatal(err)
	}
	contains(t, src,
		"v := model.User{ Name: f.g.Name(), }",
		"v.CreatedAt = f.g.Now()",
		"v.Version = int(f.g.Int64Range(1, 9))",
	)

	shadowed := strings.Replace(model, "Name string", "CreatedAt string\n\tName string", 1)
	_, _, err = generateModel(t, shadowed)
	if err == nil || !strings.Contains(err.Error(), "is shadowed by another field of the same name") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestEmbeddedPointerWithTags(t *testing.T) {
	model := "package model\n\ntype Audit struct {\n\tBy string `fixture:\"name\"`\n}\n\n//datlas:fixture\ntype User struct {\n\t*Audit\n}\n"
	_, _, err := generateModel(t, model)
	if err == nil || !strings.Contains(err.Error(), "embedded pointer *example.com/datlastest/model.Audit has fixture tags but is not generated") {
		t.Fatalf("unexpected error: %v", err)
	}
	// Opting out explicitly is fine.
	if _, _, err := generateModel(t, strings.Replace(model, "\t*Audit\n", "\t*Audit `fixture:\"-\"`\n", 1)); err != nil {
		t.Fatal(err)
	}
}

func TestEnumAliasConstants(t *testing.T) {
	model := "package model\n\n//datlas:enum\ntype Level int\n\nconst (\n\tLow Level = iota - 1\n\tMid\n\tHigh\n\tDefault = Mid\n)\n\n//datlas:fixture\ntype T struct {\n\tL Level `fixture:\"enum\"`\n}\n"
	_, src, err := generateModel(t, model)
	if err != nil {
		t.Fatal(err)
	}
	contains(t, src, "[]model.Level{0, 1, -1}[f.g.IntN(3)]")
}
