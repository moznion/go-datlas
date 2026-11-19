package declarative

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moznion/go-datlas/internal/generator/testutil"
)

// textKeySchema has a logical key of a type decoded with encoding.TextUnmarshaler: ID is a
// two-byte identifier written in hex, so "0a0b" and "0A0B" denote the same value.
const textKeySchema = `package data

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

//datlas:master items
type Item struct {
	ID   ID     ` + "`yaml:\"id\" master:\"key\"`" + `
	Name string ` + "`yaml:\"name\"`" + `
}

//datlas:master grants
type Grant struct {
	Code  string ` + "`yaml:\"code\" master:\"key\"`" + `
	Item  ID     ` + "`yaml:\"item\" master:\"ref=items\"`" + `
	Items []ID   ` + "`yaml:\"items\" master:\"ref=items\"`" + `
}

//datlas:master stocks
type Stock struct {
	Shop string ` + "`yaml:\"shop\" master:\"key\"`" + `
	Item ID     ` + "`yaml:\"item\" master:\"key\"`" + `
	N    int    ` + "`yaml:\"n\"`" + `
}
`

const textKeyItems = "- {id: 0a0b, name: a}\n- {id: 0c0d, name: b}\n"

// goTest runs the tests of the generated package, which exercise the generated code at run time.
func goTest(t *testing.T, dir string) {
	t.Helper()
	cmd := exec.Command("go", "test", "-count=1", ".")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go test failed: %v\n%s", err, out)
	}
}

func TestTextKey(t *testing.T) {
	path, err := generate(t, MasterData, map[string]string{
		"schema.go":  textKeySchema,
		"items.yaml": textKeyItems,
		// References are compared by decoded value, so the case of the text does not matter.
		"grants.yaml": "- {code: g, item: 0A0B, items: [0c0d, 0a0b]}\n",
		"stocks.yaml": "- {shop: s, item: 0a0b, n: 1}\n- {shop: s, item: 0c0d, n: 2}\n- {shop: t, item: 0A0B, n: 3}\n",
		"lookup_test.go": `package data

import "testing"

func TestLookup(t *testing.T) {
	for _, text := range []string{"0a0b", "0A0B"} {
		var id ID
		if err := id.UnmarshalText([]byte(text)); err != nil {
			t.Fatal(err)
		}
		if item, ok := ItemByID(id); !ok || item.Name != "a" {
			t.Errorf("ItemByID(%s) = %v, %v", text, item, ok)
		}
	}
	if _, ok := ItemByID(ID{9, 9}); ok {
		t.Error("ItemByID found a missing key")
	}
	if s, ok := StockByShopAndItem("t", ID{0x0a, 0x0b}); !ok || s.N != 3 {
		t.Errorf("StockByShopAndItem = %v, %v", s, ok)
	}
	if _, ok := StockByShopAndItem("t", ID{0x0c, 0x0d}); ok {
		t.Error("StockByShopAndItem found a missing key")
	}
}
`,
	})
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(path)
	testutil.TypeCheck(t, dir, ".")
	src := testutil.ReadFile(t, path)
	mustContain(t, src,
		"func ItemByID(key ID) (Item, bool) {",
		"var datlasMasterItemsIndex = sync.OnceValue(func() map[ID]func() Item {",
		`datlasMasterText[ID]("0a0b"): datlasMasterItems0,`,
		"func StockByShopAndItem(shop string, item ID) (Stock, bool) {",
		"type datlasMasterStocksKey struct { k0 string k1 ID }",
		`datlasMasterStocksKey{"s", datlasMasterText[ID]("0a0b")}: datlasMasterStocks0,`,
	)
	// No constants for keys that are not literals, and no marker for references from other
	// packages, which read the keys back as literals.
	if strings.Contains(src, "Logical keys of Items") || strings.Contains(src, keysMarker+"items") {
		t.Errorf("unexpected key constants or keys marker:\n%s", src)
	}
	goTest(t, dir)
}

func TestTextKeyErrors(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{
			name:  "keys differing only in case are duplicates",
			files: map[string]string{"items.yaml": "- {id: 0a0b, name: a}\n- {id: 0A0B, name: b}\n"},
			want:  `items.yaml:2:3: duplicate logical key ID = "0A0B" (first defined at `,
		},
		{
			name: "composite keys differing only in case are duplicates",
			files: map[string]string{
				"items.yaml":  textKeyItems,
				"stocks.yaml": "- {shop: s, item: 0a0b, n: 1}\n- {shop: s, item: 0A0B, n: 2}\n",
			},
			want: `stocks.yaml:2:3: duplicate logical key Shop, Item = "s", "0A0B" (first defined at `,
		},
		{
			name: "reference to a missing key",
			files: map[string]string{
				"items.yaml":  textKeyItems,
				"grants.yaml": "- {code: g, item: 0a0b, items: [0c0d, 0e0f]}\n",
			},
			want: `grants.yaml:1:39: Items refers to "0e0f", which does not exist in dataset "items"`,
		},
		{
			// Logical keys are always nonzero, so a key text that decodes to the zero value is rejected (#11).
			name:  "key decoding to the zero value",
			files: map[string]string{"items.yaml": "- {id: '0000', name: a}\n"},
			want:  `items.yaml:1:8: field "id" must not be empty or zero`,
		},
		{
			name:  "invalid key text is reported once, as invalid",
			files: map[string]string{"items.yaml": "- {id: 0a0, name: a}\n- {id: 0a0b, name: b}\n"},
			want:  `invalid data.ID "0a0": must be 4 hex digits`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files := map[string]string{"schema.go": textKeySchema, "items.yaml": "[]\n", "grants.yaml": "[]\n", "stocks.yaml": "[]\n"}
			for k, v := range tt.files {
				files[k] = v
			}
			_, err := generate(t, MasterData, files)
			mustFail(t, err, tt.want)
			if strings.Contains(err.Error(), "duplicate") && strings.Contains(tt.want, "invalid") {
				t.Errorf("an invalid key was also reported as a duplicate: %v", err)
			}
		})
	}
}

func TestTextKeySchemaErrors(t *testing.T) {
	const textType = `package data

import "strings"

// Tags is decoded from text but is not comparable.
type Tags struct{ v []string }

func (t *Tags) UnmarshalText(b []byte) error { t.v = strings.Split(string(b), ","); return nil }

type ID [2]byte

func (id *ID) UnmarshalText(b []byte) error { copy(id[:], b); return nil }

type Other [2]byte

func (o *Other) UnmarshalText(b []byte) error { copy(o[:], b); return nil }
`
	tests := []struct {
		name   string
		schema string
		want   string
	}{
		{
			name:   "key of a non-comparable text type",
			schema: "//datlas:master items\ntype Item struct {\n\tTags Tags `master:\"key\"`\n}\n",
			want:   "a logical key of a type implementing encoding.TextUnmarshaler must be comparable, not example.com/datlastest.Tags",
		},
		{
			name:   "reference of another text type",
			schema: "//datlas:master items\ntype Item struct {\n\tID ID `master:\"key\"`\n}\n\n//datlas:master grants\ntype Grant struct {\n\tCode string `master:\"key\"`\n\tItem Other `master:\"ref=items\"`\n}\n",
			want:   `reference of type example.com/datlastest.Other cannot refer to the key of type example.com/datlastest.ID of dataset "items"`,
		},
		{
			name:   "reference of a string type to a text key",
			schema: "//datlas:master items\ntype Item struct {\n\tID ID `master:\"key\"`\n}\n\n//datlas:master grants\ntype Grant struct {\n\tCode string `master:\"key\"`\n\tItem string `master:\"ref=items\"`\n}\n",
			want:   `reference of string type cannot refer to the text key of dataset "items"`,
		},
		{
			name:   "reference of a text type to master data of another package",
			schema: "//datlas:master grants\ntype Grant struct {\n\tCode string `master:\"key\"`\n\tItem ID `master:\"ref=example.com/other.items\"`\n}\n",
			want:   "a reference to master data of another package cannot be of a type implementing encoding.TextUnmarshaler (example.com/datlastest.ID) yet",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := generate(t, MasterData, map[string]string{
				"schema.go":   textType + "\n" + tt.schema,
				"items.yaml":  "[]\n",
				"grants.yaml": "[]\n",
			})
			mustFail(t, err, tt.want)
		})
	}
}

// A text key works the same way in seed data, where references stay within an environment.
func TestTextKeySeed(t *testing.T) {
	schema := strings.ReplaceAll(strings.ReplaceAll(textKeySchema, "datlas:master", "datlas:seed"), "master:", "seed:")
	path, err := generate(t, SeedData, map[string]string{
		"schema.go":         schema,
		"demo/items.yaml":   textKeyItems,
		"demo/grants.yaml":  "- {code: g, item: 0A0B, items: []}\n",
		"demo/stocks.yaml":  "[]\n",
		"local/items.yaml":  "- {id: 0e0f, name: c}\n",
		"local/grants.yaml": "[]\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	testutil.TypeCheck(t, filepath.Dir(path), ".")
	mustContain(t, testutil.ReadFile(t, path), "func DemoItemByID(key ID) (Item, bool) {")

	// A reference resolves only within its environment.
	_, err = generate(t, SeedData, map[string]string{
		"schema.go":         schema,
		"demo/items.yaml":   textKeyItems,
		"demo/grants.yaml":  "[]\n",
		"local/items.yaml":  "- {id: 0e0f, name: c}\n",
		"local/grants.yaml": "- {code: g, item: 0a0b, items: []}\n",
		"demo/stocks.yaml":  "[]\n",
	})
	mustFail(t, err, `Item refers to "0a0b", which does not exist in dataset "items" of the "local" environment`)
}
