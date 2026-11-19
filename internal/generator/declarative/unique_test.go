package declarative

import (
	"os"
	"strings"
	"testing"
)

const uniqueSchema = `package data

//datlas:seed shops
type Shop struct {
	UUID      string   ` + "`yaml:\"uuid\" seed:\"key\"`" + `
	Company   int      ` + "`yaml:\"company\" datlas:\"unique=shop\"`" + `
	ShopID    int      ` + "`yaml:\"shop_id\" datlas:\"unique=shop\"`" + `
	Code      string   ` + "`yaml:\"code\" datlas:\"nonzero,unique\"`" + `
	Units     []string ` + "`yaml:\"units\" datlas:\"unique\"`" + `
	Alias     string   ` + "`yaml:\"alias,omitempty\" datlas:\"unique\"`" + `
}
`

func TestUnique(t *testing.T) {
	ok := "- {uuid: a, company: 1, shop_id: 1, code: x, units: [u1, u2]}\n" +
		"- {uuid: b, company: 1, shop_id: 2, code: y, units: [u3], alias: p}\n" +
		// Absent values are not compared.
		"- {uuid: c, company: 2, shop_id: 1, code: z, units: []}\n"
	path, err := generate(t, SeedData, map[string]string{
		"schema.go":       uniqueSchema,
		"demo/shops.yaml": ok,
		// Uniqueness is per environment: the same values in another environment are fine.
		"local/shops.yaml": ok,
	})
	if err != nil {
		t.Fatal(err)
	}

	// The constraint only validates: the generated code is the same as without it.
	plain := strings.NewReplacer(` datlas:"unique=shop"`, "", ` datlas:"nonzero,unique"`, ` datlas:"nonzero"`, ` datlas:"unique"`, "").Replace(uniqueSchema)
	if plain == uniqueSchema {
		t.Fatal("the schema without constraints is the same; the test is broken")
	}
	plainPath, err := generate(t, SeedData, map[string]string{"schema.go": plain, "demo/shops.yaml": ok, "local/shops.yaml": ok})
	if err != nil {
		t.Fatal(err)
	}
	if a, b := readFile(t, path), readFile(t, plainPath); a != b {
		t.Errorf("unique changed the generated code:\n%s\n---\n%s", a, b)
	}

	// Each case lists parts of the error: the position of the duplicate, the message, and the position
	// of the first value (the file paths in the message are absolute).
	for data, want := range map[string][]string{
		// A single field.
		"- {uuid: a, company: 1, shop_id: 1, code: x, units: []}\n- {uuid: b, company: 1, shop_id: 2, code: x, units: []}\n": {
			`demo/shops.yaml:2:43: duplicate unique field Code = "x" (first defined at `, `demo/shops.yaml:1:43)`},
		// A group: each field alone may repeat, the combination may not.
		"- {uuid: a, company: 1, shop_id: 1, code: x, units: []}\n- {uuid: b, company: 1, shop_id: 1, code: y, units: []}\n": {
			`demo/shops.yaml:2:3: duplicate unique group "shop" (Company, ShopID) = 1, 1 (first defined at `, `demo/shops.yaml:1:3)`},
		// Slice elements across records.
		"- {uuid: a, company: 1, shop_id: 1, code: x, units: [u1]}\n- {uuid: b, company: 1, shop_id: 2, code: y, units: [u2, u1]}\n": {
			`demo/shops.yaml:2:58: duplicate element of unique field Units = "u1" (first defined at `, `demo/shops.yaml:1:54)`},
		// Slice elements within one record.
		"- {uuid: a, company: 1, shop_id: 1, code: x, units: [u1, u1]}\n": {
			`demo/shops.yaml:1:58: duplicate element of unique field Units = "u1" (first defined at `, `demo/shops.yaml:1:54)`},
		// An omitempty field present in two records.
		"- {uuid: a, company: 1, shop_id: 1, code: x, units: [], alias: p}\n- {uuid: b, company: 1, shop_id: 2, code: y, units: [], alias: p}\n": {
			`duplicate unique field Alias = "p"`},
	} {
		_, err := generate(t, SeedData, map[string]string{"schema.go": uniqueSchema, "demo/shops.yaml": data, "local/shops.yaml": ok})
		for _, w := range want {
			mustFail(t, err, w)
		}
	}
}

func TestUniqueSchemaErrors(t *testing.T) {
	for field, want := range map[string]string{
		"X Money `yaml:\"x\" datlas:\"unique\"`":                "unique is not supported for data.Money: values of types implementing encoding.TextUnmarshaler can only be compared after decoding",
		"X []Money `yaml:\"x\" datlas:\"unique\"`":              "unique is not supported for data.Money",
		"X float64 `yaml:\"x\" datlas:\"unique\"`":              "unique requires a string, integer or bool type (or a slice of one), not float64",
		"X *string `yaml:\"x\" datlas:\"unique\"`":              "unique requires a string, integer or bool type (or a slice of one), not *string",
		"X []string `yaml:\"x\" datlas:\"unique=g\"`":           "unique requires a string, integer or bool type, not []string",
		"X time.Time `yaml:\"x\" datlas:\"unique\"`":            "unique requires a string, integer or bool type (or a slice of one), not time.Time",
		"X struct{ Y string `datlas:\"unique\"` } `yaml:\"x\"`": "unique must be on a field of Price or of a struct inlined into it",
		"X string `yaml:\"x\" datlas:\"unique=\"`":              `invalid unique group name ""`,
		"X string `yaml:\"x\" datlas:\"unique=1a\"`":            `invalid unique group name "1a"`,
		"X string `yaml:\"x\" datlas:\"unique,unique=g\"`":      "has both unique and unique=g",
		"X string `yaml:\"x\" datlas:\"uniq\"`":                 `unknown datlas tag option "uniq" (available: nonzero, unique, unique=<group>)`,
	} {
		src := strings.Replace(moneySchema, "\tRaw    []byte", "\t"+field+"\n\tRaw    []byte", 1)
		src = strings.Replace(src, "import (\n", "import (\n\t\"time\"\n", 1)
		if !strings.Contains(src, field) {
			t.Fatalf("%s: the field was not inserted", field)
		}
		if !strings.Contains(field, "time.Time") {
			src = strings.Replace(src, "\t\"time\"\n", "", 1)
		}
		_, err := generate(t, MasterData, map[string]string{"schema.go": src, "prices.yaml": "[]\n"})
		mustFail(t, err, want)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
