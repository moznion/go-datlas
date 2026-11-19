package declarative

import (
	"strings"
	"testing"
)

// uniqueTextSchema has unique fields of a type decoded with encoding.TextUnmarshaler: ID is written
// in hex, so "0a0b" and "0A0B" are different texts denoting the same value.
const uniqueTextSchema = `package data

import (
	"encoding/hex"
	"errors"
)

type ID [2]byte

func (id *ID) UnmarshalText(b []byte) error {
	if len(b) != 4 {
		return errors.New("must be 4 hex digits")
	}
	_, err := hex.Decode(id[:], b)
	return err
}

//datlas:seed shops
type Shop struct {
	Code    string ` + "`yaml:\"code\" seed:\"key\"`" + `
	Owner   ID     ` + "`yaml:\"owner\" datlas:\"unique\"`" + `
	Units   []ID   ` + "`yaml:\"units\" datlas:\"unique\"`" + `
	Company int    ` + "`yaml:\"company\" datlas:\"unique=shop\"`" + `
	Ext     ID     ` + "`yaml:\"ext\" datlas:\"unique=shop\"`" + `
	Alias   ID     ` + "`yaml:\"alias,omitempty\" datlas:\"unique\"`" + `
}
`

// unique on types decoded with encoding.TextUnmarshaler compares the decoded values.
func TestUniqueTextUnmarshaler(t *testing.T) {
	ok := "- {code: a, owner: 0a0b, units: [0001, 0002], company: 1, ext: 0a0b}\n" +
		// The same ext in another company is fine; an absent alias is not compared.
		"- {code: b, owner: 0c0d, units: [0003], company: 2, ext: 0a0b, alias: 0e0f}\n" +
		"- {code: c, owner: 0e0f, units: [], company: 3, ext: 0a0b}\n"
	if _, err := generate(t, SeedData, map[string]string{
		"schema.go":       uniqueTextSchema,
		"demo/shops.yaml": ok,
		// Uniqueness is per environment.
		"local/shops.yaml": ok,
	}); err != nil {
		t.Fatal(err)
	}

	for data, want := range map[string][]string{
		// Texts differing only in case denote the same value.
		"- {code: a, owner: 0a0b, units: [], company: 1, ext: 0001}\n- {code: b, owner: 0A0B, units: [], company: 2, ext: 0001}\n": {
			`demo/shops.yaml:2:20: duplicate unique field Owner = "0A0B" (first defined at `, `demo/shops.yaml:1:20)`},
		// Slice elements across records and within one record.
		"- {code: a, owner: 0001, units: [0a0b], company: 1, ext: 0001}\n- {code: b, owner: 0002, units: [0003, 0A0B], company: 2, ext: 0001}\n": {
			`demo/shops.yaml:2:40: duplicate element of unique field Units = "0A0B" (first defined at `, `demo/shops.yaml:1:34)`},
		"- {code: a, owner: 0001, units: [0a0b, 0A0B], company: 1, ext: 0001}\n": {
			`demo/shops.yaml:1:40: duplicate element of unique field Units = "0A0B" (first defined at `},
		// A group mixing a text field with an integer field.
		"- {code: a, owner: 0001, units: [], company: 1, ext: 0a0b}\n- {code: b, owner: 0002, units: [], company: 1, ext: 0A0B}\n": {
			`demo/shops.yaml:2:3: duplicate unique group "shop" (Company, Ext) = 1, "0A0B" (first defined at `},
		// An omitempty field present in two records.
		"- {code: a, owner: 0001, units: [], company: 1, ext: 0001, alias: 0a0b}\n- {code: b, owner: 0002, units: [], company: 2, ext: 0001, alias: 0A0B}\n": {
			`duplicate unique field Alias = "0A0B"`},
	} {
		_, err := generate(t, SeedData, map[string]string{"schema.go": uniqueTextSchema, "demo/shops.yaml": data, "local/shops.yaml": ok})
		for _, w := range want {
			mustFail(t, err, w)
		}
	}

	// A value that fails to decode is reported as invalid, not compared.
	_, err := generate(t, SeedData, map[string]string{
		"schema.go":       uniqueTextSchema,
		"demo/shops.yaml": "- {code: a, owner: 0a0, units: [], company: 1, ext: 0001}\n- {code: b, owner: 0a0, units: [], company: 2, ext: 0002}\n",
	})
	mustFail(t, err, `invalid data.ID "0a0": must be 4 hex digits`)
	if strings.Contains(err.Error(), "duplicate") {
		t.Errorf("an invalid value was also reported as a duplicate: %v", err)
	}
}

func TestUniqueTextUnmarshalerSchemaErrors(t *testing.T) {
	schema := strings.Replace(uniqueTextSchema, "//datlas:seed shops", `// Tags is decoded from text but is not comparable.
type Tags struct{ v []string }

func (t *Tags) UnmarshalText(b []byte) error { t.v = []string{string(b)}; return nil }

//datlas:seed shops`, 1)
	schema = strings.Replace(schema, "\tAlias ", "\tTags    Tags   `yaml:\"tags\" datlas:\"unique\"`\n\tAlias ", 1)
	_, err := generate(t, SeedData, map[string]string{"schema.go": schema, "demo/shops.yaml": "[]\n"})
	mustFail(t, err, "unique on a type implementing encoding.TextUnmarshaler requires a comparable type, not data.Tags")
}
