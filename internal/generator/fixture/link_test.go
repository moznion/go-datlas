package fixture

import (
	"strings"
	"testing"

	"github.com/moznion/go-datlas/internal/generator/testutil"
)

const linkModel = `package model

import "database/sql"

type UUID [16]byte

type Timestamps struct {
	OwnerID UUID ` + "`fixture:\"from=Company.ID\"`" + `
}

//datlas:fixture
type Company struct {
	ID   UUID   ` + "`fixture:\"uuid\"`" + `
	Code string ` + "`fixture:\"alpha\"`" + `
}

//datlas:fixture
type User struct {
	ID        UUID           ` + "`fixture:\"uuid\"`" + `
	Company   Company        ` + "`fixture:\"nested\"`" + `
	CompanyID UUID           ` + "`fixture:\"from=Company.ID,ref=Company.ID\"`" + `
	CodePtr   *string        ` + "`fixture:\"from=Company.Code\"`" + `
	CodeNull  sql.NullString ` + "`fixture:\"from=Company.Code,wrap=example.com/datlastest/model.NullString\"`" + `
	InviterID *UUID          ` + "`fixture:\"uuid,ref=User.ID\"`" + `
	Code      sql.NullString ` + "`fixture:\"alpha,wrap=example.com/datlastest/model.NullString,ref=Company.Code\"`" + `
	Timestamps
}

func NullString(s string) sql.NullString { return sql.NullString{String: s, Valid: true} }
`

func TestFromAndRef(t *testing.T) {
	_, src, err := generateModel(t, linkModel)
	if err != nil {
		t.Fatal(err)
	}
	contains(t, src,
		// Copies are not part of the literal; they follow it, before opts.
		"Timestamps: model.Timestamps{},",
		`v.CompanyID = v.Company.ID
		v.CodePtr = fixturekit.Ptr(v.Company.Code)
		v.CodeNull = model.NullString(v.Company.Code)
		v.Timestamps.OwnerID = v.Company.ID
		for _, opt := range opts {`,
		`func UserCompanyIDFrom(src model.Company) func(*model.User) { return func(v *model.User) { v.CompanyID = src.ID } }`,
		`func UserInviterIDFrom(src model.User) func(*model.User) { return func(v *model.User) { v.InviterID = fixturekit.Ptr(src.ID) } }`,
		`func UserCodeFrom(src model.Company) func(*model.User) { return func(v *model.User) { v.Code = model.NullString(src.Code) } }`,
	)
	if strings.Contains(src, "CompanyID: ") {
		t.Errorf("copied field is part of the literal:\n%s", src)
	}
}

func TestRefInPolicyFile(t *testing.T) {
	dir := testutil.WriteTree(t, map[string]string{
		"gen/gen.go": "package gen\n\ntype Account struct {\n\tID [16]byte\n}\n\ntype Member struct {\n\tAccountID [16]byte\n}\n",
		"fx/fixture.yaml": "types:\n  - type: example.com/datlastest/gen.Account\n    fields:\n      ID: uuid\n" +
			"  - type: example.com/datlastest/gen.Member\n    fields:\n      AccountID: uuid,ref=Account.ID\n",
	})
	path, err := run(Config{Dir: dir, OutDir: "fx", PolicyFile: "fx/fixture.yaml"})
	if err != nil {
		t.Fatal(err)
	}
	testutil.TypeCheck(t, dir, "./...")
	contains(t, testutil.ReadFile(t, path),
		`func MemberAccountIDFrom(src gen.Account) func(*gen.Member) { return func(v *gen.Member) { v.AccountID = src.ID } }`)
}

func TestFromAndRefErrors(t *testing.T) {
	model := func(fields string) string {
		return "package model\n\n" +
			"type Inner struct {\n\tID int\n\tsecret int\n}\n\n" +
			"//datlas:fixture\ntype Company struct {\n\tID int `fixture:\"int\"`\n\tName string `fixture:\"word\"`\n}\n\n" +
			"//datlas:fixture\ntype T struct {\n\tCompany Company `fixture:\"nested\"`\n\tP *Company `fixture:\"nested\"`\n\tInner Inner\n" + fields + "}\n"
	}
	tests := map[string]string{
		"A int `fixture:\"from\"`":                                          `fixture generator "from" requires a field path`,
		"A int `fixture:\"from=Company.Missing\"`":                          "model.Company has no field Missing (in field path Company.Missing)",
		"A int `fixture:\"from=P.ID\"`":                                     "field path P.ID goes through the pointer *example.com/datlastest/model.Company",
		"A string `fixture:\"from=Company.ID\"`":                            "field Company.ID is int, which cannot be assigned to string",
		"A []int `fixture:\"from=Company.ID,count=2\"`":                     `parameter "count" cannot be combined with "from"`,
		"A int `fixture:\"from=Company.ID,unique\"`":                        `fixture generator "from" cannot be combined with "unique"`,
		"A int `fixture:\"from=Company.ID\"`\n\tB int `fixture:\"from=A\"`": "from=A copies a field that is itself set with from",
		"A int `fixture:\"int,ref=Missing.ID\"`":                            "ref=Missing.ID: no factory method Missing (available: Company, T)",
		"A int `fixture:\"from=Inner.secret\"`":                             "field secret of field path Inner.secret is unexported and cannot be read from package example.com/datlastest/fx",
		"A int `fixture:\"int,ref=Company\"`":                               `parameter "ref" takes a factory and a field path`,
		"A string `fixture:\"word,ref=Company.ID\"`":                        "ref=Company.ID: field ID is int, which cannot be assigned to string",
	}
	for fields, want := range tests {
		_, _, err := generateModel(t, model("\t"+fields+"\n"))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: error %v does not contain %q", fields, err, want)
		}
	}
}

func TestRefOptionConflict(t *testing.T) {
	dir := testutil.WriteTree(t, map[string]string{
		"model/model.go": "package model\n\n//datlas:fixture\ntype Company struct {\n\tID int `fixture:\"int\"`\n}\n\n" +
			"//datlas:fixture\ntype User struct {\n\tCompanyID int `fixture:\"int,ref=Company.ID\"`\n}\n",
		"fx/own.go": "package fx\n\nfunc UserCompanyIDFrom() {}\n",
	})
	_, err := run(Config{Dir: dir, Patterns: []string{"./model"}, OutDir: "fx"})
	if err == nil || !strings.Contains(err.Error(), "UserCompanyIDFrom is already declared in the output package") {
		t.Fatalf("unexpected error %v", err)
	}
}
