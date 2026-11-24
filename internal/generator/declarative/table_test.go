package declarative

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/moznion/go-datlas/internal/generator/testutil"
)

const tableSchema = `package data

import (
	"database/sql/driver"
	"strings"
	"time"
)

type Code string

func (c *Code) UnmarshalText(b []byte) error { *c = Code(strings.ToLower(string(b))); return nil }
func (c Code) MarshalText() ([]byte, error)  { return []byte(c), nil }

type Money struct{ Yen int }

func (m *Money) Value() (driver.Value, error) { return int64(m.Yen), nil }

type Base struct {
	CreatedAt time.Time ` + "`yaml:\"created_at\"`" + `
}

//datlas:master items table=app.items
type Item struct {
	Base     ` + "`yaml:\",inline\"`" + `
	ID       Code              ` + "`yaml:\"id\" master:\"key\"`" + `
	Alias    *Code             ` + "`yaml:\"alias\"`" + `
	Name     string            ` + "`yaml:\"name\" db:\"title\"`" + `
	Price    Money             ` + "`yaml:\"price\"`" + `
	Tags     []string          ` + "`yaml:\"tags\"`" + `
	Attrs    *map[string]int   ` + "`yaml:\"attrs\"`" + `
	TTL      time.Duration     ` + "`yaml:\"ttl\"`" + `
	Note     string            ` + "`yaml:\"note\" db:\"-\"`" + `
}

//datlas:master pairs table=pairs prune=false
type Pair struct {
	A string ` + "`yaml:\"a\" master:\"key\"`" + `
	B string ` + "`yaml:\"b\" master:\"key\"`" + `
}

//datlas:master plain singular=PlainOne
type PlainRow struct {
	K string ` + "`yaml:\"k\" master:\"key\"`" + `
}
`

func TestApplyMasterData(t *testing.T) {
	dir := testutil.WriteTree(t, map[string]string{
		"schema.go":  tableSchema,
		"items.yaml": "- {id: A, alias: X, created_at: 2024-01-01, name: n, price: {yen: 1}, tags: [t], ttl: 1h, note: x}\n- {id: b, created_at: 2024-01-01, name: m, price: {yen: 2}, tags: [], attrs: {a: 1}, ttl: 0s, note: y}\n",
		"pairs.yaml": "- {a: x, b: y}\n",
		"plain.yaml": "- {k: z}\n",
	})
	path, err := run(MasterData, Config{Dir: dir, Package: "."})
	if err != nil {
		t.Fatal(err)
	}
	testutil.TypeCheck(t, dir, ".")
	src := testutil.ReadFile(t, path)
	mustContain(t, src,
		"func ApplyMasterData(ctx context.Context, db interface {",
		"datlasMasterUpsertItems, datlasMasterUpsertPairs, datlasMasterPruneItems, }",
		`INSERT INTO "app"."items" ("created_at", "id", "alias", "title", "price", "tags", "attrs", "ttl") VALUES ($1, $2, $3, $4, $5, $6, $7, $8) ON CONFLICT ("id") DO UPDATE SET`,
		`DELETE FROM "app"."items" WHERE "id" NOT IN ($1, $2)`,
		`keys = append(keys, args[1])`,
		`INSERT INTO "pairs" ("a", "b") VALUES ($1, $2) ON CONFLICT ("a", "b") DO NOTHING`,
		"datlasCol1, err := datlasRec.ID.MarshalText()",
		"if datlasRec.Alias != nil {",
		"&datlasRec.Price",
		"json.Marshal(datlasRec.Tags)",
		"if datlasRec.Attrs != nil {",
		"return []any{datlasRec.Base.CreatedAt, string(datlasCol1), datlasCol2, datlasRec.Name, &datlasRec.Price, string(datlasCol5), datlasCol6, datlasRec.TTL}, nil",
	)
	for _, absent := range []string{"datlasMasterPrunePairs", "datlasMasterUpsertPlain", `"note"`} {
		if strings.Contains(src, absent) {
			t.Errorf("generated code contains %s", absent)
		}
	}
}

func TestApplyUpsertOptions(t *testing.T) {
	schema := "package data\n\n//datlas:master roles table=roles prune=false\ntype R struct {\n" +
		"\tID string `yaml:\"id\" master:\"key\"`\n\tLabel string `yaml:\"label\" db:\",insertonly\"`\n\tName string `yaml:\"name\" db:\"role_name,insertonly\"`\n\tPerm []string `yaml:\"perm\"`\n}\n"
	dir := testutil.WriteTree(t, map[string]string{"schema.go": schema, "roles.yaml": "- {id: a, label: A, name: n, perm: [x]}\n"})
	path, err := run(MasterData, Config{Dir: dir, Package: "."})
	if err != nil {
		t.Fatal(err)
	}
	testutil.TypeCheck(t, dir, ".")
	mustContain(t, testutil.ReadFile(t, path),
		`INSERT INTO "roles" ("id", "label", "role_name", "perm") VALUES ($1, $2, $3, $4) ON CONFLICT ("id") DO UPDATE SET "perm" = EXCLUDED."perm"`,
	)
}

func TestApplyMasterDataAbsentWithoutTables(t *testing.T) {
	path, err := generate(t, MasterData, map[string]string{"schema.go": roleSchema, "roles.yaml": "[]\n"})
	if err != nil {
		t.Fatal(err)
	}
	if src := testutil.ReadFile(t, path); strings.Contains(src, "ApplyMasterData") || strings.Contains(src, "database/sql") {
		t.Fatalf("generated Apply without tables:\n%s", src)
	}
}

func TestApplySeedData(t *testing.T) {
	schema := "package data\n\n//datlas:seed companies table=companies prune=true\ntype Company struct {\n\tKey string `yaml:\"key\" seed:\"key\"`\n}\n\n" +
		"//datlas:seed events table=events\ntype Event struct {\n\tCompany string `yaml:\"company\" seed:\"ref=companies\"`\n}\n"
	dir := testutil.WriteTree(t, map[string]string{
		"schema.go":            schema,
		"local/companies.yaml": "- {key: a}\n- {key: b}\n",
		"local/events.yaml":    "- {company: a}\n",
		"demo/events.yaml":     "[]\n",
	})
	path, err := run(SeedData, Config{Dir: dir, Package: "."})
	if err != nil {
		t.Fatal(err)
	}
	testutil.TypeCheck(t, dir, ".")
	mustContain(t, testutil.ReadFile(t, path),
		"func ApplySeedData(ctx context.Context, db interface {",
		// demo has no companies.yaml, so its companies table is not pruned.
		`case "demo": steps = []func(context.Context, datlasSeedDB) error{ datlasSeedUpsertDemoCompanies, datlasSeedUpsertDemoEvents, }`,
		`case "local": steps = []func(context.Context, datlasSeedDB) error{ datlasSeedUpsertLocalCompanies, datlasSeedUpsertLocalEvents, datlasSeedPruneLocalCompanies, }`,
		`return fmt.Errorf("datlas: no seed data for environment %q", env)`,
		"INSERT INTO \"events\" (\"company\") VALUES ($1)`",
		`DELETE FROM "companies" WHERE "key" NOT IN ($1, $2)`,
	)
}

func TestTableErrors(t *testing.T) {
	master := func(directive, fields string) map[string]string {
		return map[string]string{
			"schema.go":  "package data\n\nimport \"strings\"\n\nvar _ = strings.ToLower\n\n" + directive + "\ntype R struct {\n\tCode string `yaml:\"code\" master:\"key\"`\n" + fields + "}\n",
			"roles.yaml": "[]\n",
		}
	}
	text := "type T struct{ s string }\n\nfunc (t *T) UnmarshalText(b []byte) error { t.s = string(b); return nil }\n"
	tests := []struct {
		name  string
		kind  Kind
		files map[string]string
		cfg   Config
		want  string
	}{
		{name: "invalid table", files: master("//datlas:master roles table=a-b", ""), want: `invalid table name "a-b"`},
		{name: "empty table", files: master("//datlas:master roles table=", ""), want: `unknown or repeated option "table="`},
		{name: "prune=true on master", files: master("//datlas:master roles table=r prune=true", ""), want: `unknown or repeated option "prune=true"`},
		{name: "prune without table", files: master("//datlas:master roles prune=false", ""), want: "prune has no effect without table=<name>"},
		{name: "duplicate column", files: master("//datlas:master roles table=r", "\tName string `yaml:\"name\" db:\"code\"`\n"), want: `column "code" is also used by Code`},
		{name: "invalid column", files: master("//datlas:master roles table=r", "\tName string `yaml:\"the-name\"`\n"), want: `invalid column name "the-name"`},
		{name: "db tag options", files: master("//datlas:master roles table=r", "\tName string `yaml:\"name\" db:\"name,omitempty\"`\n"), want: `unknown db tag option "omitempty" (available: insertonly)`},
		{name: "repeated insertonly", files: master("//datlas:master roles table=r", "\tName string `yaml:\"name\" db:\",insertonly,insertonly\"`\n"), want: `repeated db tag option "insertonly"`},
		{name: "trailing comma", files: master("//datlas:master roles table=r", "\tName string `yaml:\"name\" db:\"name,\"`\n"), want: `empty db tag option in "name," (available: insertonly)`},
		{name: "trailing comma after insertonly", files: master("//datlas:master roles table=r", "\tName string `yaml:\"name\" db:\"name,insertonly,\"`\n"), want: `empty db tag option in "name,insertonly,"`},
		{name: "insertonly keyless seed", kind: SeedData, files: map[string]string{
			"schema.go":         "package data\n\n//datlas:seed events table=events\ntype E struct {\n\tA string `db:\",insertonly\"`\n}\n",
			"local/events.yaml": "[]\n",
		}, want: "E.A: insertonly requires a logical key"},
		{name: "insertonly key", files: map[string]string{
			"schema.go":  "package data\n\n//datlas:master roles table=r\ntype R struct {\n\tCode string `yaml:\"code\" master:\"key\" db:\",insertonly\"`\n\tName string `yaml:\"name\"`\n}\n",
			"roles.yaml": "[]\n",
		}, want: "R.Code: a logical key field cannot be insertonly"},
		{name: "insertonly inline", files: map[string]string{
			"schema.go":  "package data\n\ntype B struct {\n\tX string `yaml:\"x\"`\n}\n\n//datlas:master roles table=r\ntype R struct {\n\tB `yaml:\",inline\" db:\",insertonly\"`\n\tCode string `yaml:\"code\" master:\"key\"`\n}\n",
			"roles.yaml": "[]\n",
		}, want: "R.B: insertonly cannot be set on an inline struct"},
		{name: "inline with a column name", files: map[string]string{
			"schema.go":  "package data\n\ntype B struct {\n\tX string `yaml:\"x\"`\n}\n\n//datlas:master roles table=r\ntype R struct {\n\tB `yaml:\",inline\" db:\"b\"`\n\tCode string `yaml:\"code\" master:\"key\"`\n}\n",
			"roles.yaml": "[]\n",
		}, want: "R.B: an inline struct has no column of its own"},
		{name: "excluded key", files: map[string]string{
			"schema.go":  "package data\n\n//datlas:master roles table=r\ntype R struct {\n\tCode string `yaml:\"code\" master:\"key\" db:\"-\"`\n\tName string `yaml:\"name\"`\n}\n",
			"roles.yaml": "[]\n",
		}, want: "a logical key field cannot be excluded from the table"},
		{name: "text without marshaler", files: func() map[string]string {
			f := master("//datlas:master roles table=r", "\tT T `yaml:\"t\"`\n")
			f["schema.go"] += "\n" + text
			return f
		}(), want: "implements encoding.TextUnmarshaler but neither driver.Valuer nor encoding.TextMarshaler"},
		{name: "duplicate table", files: map[string]string{
			"schema.go": "package data\n\n//datlas:master a table=t\ntype A struct {\n\tK string `master:\"key\"`\n}\n\n//datlas:master b table=t\ntype B struct {\n\tK string `master:\"key\"`\n}\n",
			"a.yaml":    "[]\n",
			"b.yaml":    "[]\n",
		}, want: `table t is also the table of dataset "a"`},
		{name: "apply name conflict", files: func() map[string]string {
			f := master("//datlas:master roles table=r", "")
			f["schema.go"] += "\nfunc ApplyMasterData() {}\n"
			return f
		}(), want: "generated identifier ApplyMasterData conflicts with the declaration"},
		{name: "seed apply name conflict", kind: SeedData, files: map[string]string{
			"schema.go":         "package data\n\n//datlas:seed events table=events\ntype E struct {\n\tA string\n}\n\nfunc ApplySeedData() {}\n",
			"local/events.yaml": "[]\n",
		}, want: "generated identifier ApplySeedData conflicts with the declaration"},
		{name: "upper-case table", files: master("//datlas:master roles table=app.Roles", ""), want: `table name: "Roles" has upper-case letters, which PostgreSQL folds to lower case unless quoted; use "roles"`},
		{name: "upper-case YAML key", files: master("//datlas:master roles table=r", "\tName string `yaml:\"displayName\"`\n"), want: `column name: "displayName" has upper-case letters`},
		{name: "upper-case db tag", files: master("//datlas:master roles table=r", "\tName string `yaml:\"name\" db:\"Name\"`\n"), want: `column name: "Name" has upper-case letters`},
		{name: "bool map key in JSON", files: master("//datlas:master roles table=r", "\tM map[string]map[bool]int `yaml:\"m\"`\n"), want: "the column is written as JSON, but encoding/json cannot encode maps with keys of type bool"},
		{name: "bool map key in unexported embedded struct", files: func() map[string]string {
			f := master("//datlas:master roles table=r", "\tO Outer `yaml:\"o\"`\n")
			f["schema.go"] += "\ntype inner struct {\n\tM map[bool]int `yaml:\"m\"`\n}\n\ntype Outer struct {\n\tinner `yaml:\"inner\"`\n\tX int `yaml:\"x\"`\n}\n"
			return f
		}(), want: "encoding/json cannot encode maps with keys of type bool"},
		{name: "NaN in JSON", files: map[string]string{
			"schema.go":  "package data\n\n//datlas:master roles table=r\ntype R struct {\n\tCode string `yaml:\"code\" master:\"key\"`\n\tF float64 `yaml:\"f\"`\n\tM map[string]float64 `yaml:\"m\"`\n}\n",
			"roles.yaml": "- {code: a, f: .nan, m: {x: .inf}}\n",
		}, want: `roles.yaml:1:29: an infinity cannot be written to column "m", which holds JSON`},
		{name: "unknown dialect", files: master("//datlas:master roles table=r", ""), cfg: Config{Dialect: "oracle"}, want: `unknown SQL dialect "oracle"`},
		{name: "prune keyless seed", kind: SeedData, files: map[string]string{
			"schema.go":         "package data\n\n//datlas:seed events table=events prune=true\ntype E struct {\n\tA string\n}\n",
			"local/events.yaml": "[]\n",
		}, want: "prune=true requires a logical key"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := tt.cfg
			cfg.Dir, cfg.Package = testutil.WriteTree(t, tt.files), "."
			_, err := run(tt.kind, cfg)
			mustFail(t, err, tt.want)
		})
	}
}

func TestJSONAllowedValues(t *testing.T) {
	schema := "package data\n\nimport \"encoding/json\"\n\ntype B bool\n\nfunc (b B) MarshalText() ([]byte, error) { return []byte(\"b\"), nil }\n\n" +
		"type Raw map[bool]int\n\nfunc (r Raw) MarshalJSON() ([]byte, error) { return json.Marshal(len(r)) }\n\n" +
		"type Ratio struct {\n\tV float64 `yaml:\"v\"`\n}\n\nfunc (r Ratio) MarshalJSON() ([]byte, error) { return []byte(\"null\"), nil }\n\n" +
		"//datlas:master roles table=r\ntype R struct {\n\tCode string `yaml:\"code\" master:\"key\"`\n" +
		"\tF float64 `yaml:\"f\"`\n\tQ []Ratio `yaml:\"q\"`\n\tT map[B]int `yaml:\"t\"`\n\tR Raw `yaml:\"r\"`\n\tX map[bool]int `yaml:\"x\" db:\"-\"`\n}\n"
	// NaN outside JSON columns or in json.Marshaler types, bool keys of a TextMarshaler, json.Marshaler types and excluded fields are fine.
	_, err := generate(t, MasterData, map[string]string{"schema.go": schema, "roles.yaml": "- {code: a, f: .nan, q: [{v: .nan}], t: {true: 1}, r: {true: 1}, x: {true: 1}}\n"})
	if err != nil {
		t.Fatal(err)
	}
}

// TestImportNamesUnchanged checks that tables do not rename the imports of the other generated code.
func TestImportNamesUnchanged(t *testing.T) {
	path, err := generate(t, MasterData, map[string]string{
		"keys/keys.go": "package keys\n\ntype K string\n",
		"schema.go":    "package data\n\nimport \"example.com/datlastest/keys\"\n\n//datlas:master roles table=r\ntype R struct {\n\tCode keys.K `yaml:\"code\" master:\"key\"`\n}\n",
		"roles.yaml":   "- {code: a}\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, testutil.ReadFile(t, path), `"example.com/datlastest/keys"`, `RoleCodeA keys.K = "a"`)
	testutil.TypeCheck(t, filepath.Dir(path), ".")
}
