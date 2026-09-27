package declarative

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moznion/go-datlas/internal/generator/output"
	"github.com/moznion/go-datlas/internal/generator/testutil"
)

const roleSchema = `package data

//datlas:master roles
type Role struct {
	Code  string ` + "`yaml:\"code\" master:\"key\"`" + `
	Name  string ` + "`yaml:\"name\"`" + `
	Level int8   ` + "`yaml:\"level,omitempty\"`" + `
}
`

func generate(t *testing.T, kind Kind, files map[string]string) (string, error) {
	t.Helper()
	dir := testutil.WriteTree(t, files)
	return run(kind, Config{Dir: dir, Package: "."})
}

// run generates and writes the file, returning its path.
func run(kind Kind, cfg Config) (string, error) {
	r, err := Generate(kind, cfg)
	if err != nil {
		return "", err
	}
	if _, err := output.Apply(r, false); err != nil {
		return "", err
	}
	return r.Path, nil
}

func TestMasterValidationErrors(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{
			name:  "unknown field",
			files: map[string]string{"roles.yaml": "- code: a\n  name: A\n  nmae: typo\n"},
			want:  `roles.yaml:3:3: unknown field "nmae"`,
		},
		{
			name:  "missing required field",
			files: map[string]string{"roles.yaml": "- code: a\n"},
			want:  `missing required field "name"`,
		},
		{
			name:  "string for integer",
			files: map[string]string{"roles.yaml": "- code: a\n  name: A\n  level: high\n"},
			want:  "expected an integer for type int8, got a string",
		},
		{
			name:  "integer for string",
			files: map[string]string{"roles.yaml": "- code: a\n  name: 123\n"},
			want:  "expected a string for type string, got an integer",
		},
		{
			name:  "integer overflow",
			files: map[string]string{"roles.yaml": "- code: a\n  name: A\n  level: 128\n"},
			want:  "128 overflows int8",
		},
		{
			name:  "malformed yaml",
			files: map[string]string{"roles.yaml": "- code: [a\n"},
			want:  "malformed YAML",
		},
		{
			name:  "not a sequence",
			files: map[string]string{"roles.yaml": "code: a\n"},
			want:  "expected a sequence of records, got a mapping",
		},
		{
			name:  "empty file",
			files: map[string]string{"roles.yaml": ""},
			want:  "expected exactly one YAML document",
		},
		{
			name:  "duplicate logical key",
			files: map[string]string{"roles.yaml": "- code: a\n  name: A\n- code: a\n  name: B\n"},
			want:  "roles.yaml:3:7: duplicate logical key Code = \"a\" (first defined at",
		},
		{
			name:  "duplicate yaml key",
			files: map[string]string{"roles.yaml": "- code: a\n  name: A\n  name: B\n"},
			want:  `mapping key "name" already defined`,
		},
		{
			name:  "missing file",
			files: map[string]string{},
			want:  `no YAML file found for dataset "roles"`,
		},
		{
			name:  "file without schema",
			files: map[string]string{"roles.yaml": "[]\n", "perms.yaml": "[]\n"},
			want:  `no //datlas:master type declares dataset "perms"`,
		},
		{
			name:  "unknown alias",
			files: map[string]string{"roles.yaml": "- code: a\n  name: *nope\n"},
			want:  "roles.yaml:2:9: unknown alias *nope",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.files["schema.go"] = roleSchema
			_, err := generate(t, MasterData, tt.files)
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error %q does not contain %q", err, tt.want)
			}
		})
	}
}

func TestSchemaErrors(t *testing.T) {
	tests := []struct {
		name   string
		schema string
		want   string
	}{
		{
			name:   "master without key",
			schema: "package data\n\n//datlas:master roles\ntype Role struct {\n\tCode string\n}\n",
			want:   "must declare a logical key",
		},
		{
			name:   "optional key",
			schema: "package data\n\n//datlas:master roles\ntype Role struct {\n\tA *string `master:\"key\"`\n}\n",
			want:   "a logical key field must be required",
		},
		{
			name:   "unsupported type",
			schema: "package data\n\n//datlas:master roles\ntype Role struct {\n\tA string `master:\"key\"`\n\tB any\n}\n",
			want:   "Role.B: unsupported type",
		},
		{
			name:   "missing name",
			schema: "package data\n\n//datlas:master\ntype Role struct {\n\tA string `master:\"key\"`\n}\n",
			want:   "requires a dataset name",
		},
		{
			name:   "directive on non-struct",
			schema: "package data\n\n//datlas:master roles\ntype Role string\n",
			want:   "can only be used on struct types",
		},
		{
			name:   "stray directive",
			schema: "package data\n\n//datlas:master roles\nvar X = 1\n",
			want:   "must be placed on a type declaration",
		},
		{
			name:   "syntax error",
			schema: "package data\n\n//datlas:master roles\ntype Role struct {\n\tA string `master:\"key\"`\n\nfunc (\n",
			want:   "schema.go:7",
		},
		{
			name:   "identifier conflict",
			schema: "package data\n\nvar Roles = 1\n\n//datlas:master roles\ntype Role struct {\n\tA string `master:\"key\"`\n}\n",
			want:   "generated identifier Roles conflicts with the declaration",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := generate(t, MasterData, map[string]string{"schema.go": tt.schema, "roles.yaml": "[]\n"})
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error %q does not contain %q", err, tt.want)
			}
		})
	}
}

func TestMasterRichTypes(t *testing.T) {
	schema := `package data

import "time"

type Level int16

type Meta struct {
	Owner string ` + "`yaml:\"owner\"`" + `
}

type Base struct {
	CreatedAt time.Time ` + "`yaml:\"created_at\"`" + `
}

//datlas:master things
type Thing struct {
	Base     ` + "`yaml:\",inline\"`" + `
	ID       uint32            ` + "`yaml:\"id\" master:\"key\"`" + `
	Level    Level             ` + "`yaml:\"level\"`" + `
	Ratio    float32           ` + "`yaml:\"ratio\"`" + `
	Max      float64           ` + "`yaml:\"max\"`" + `
	Enabled  bool              ` + "`yaml:\"enabled\"`" + `
	Tags     []string          ` + "`yaml:\"tags\"`" + `
	Pair     [2]int            ` + "`yaml:\"pair\"`" + `
	Labels   map[string]int    ` + "`yaml:\"labels\"`" + `
	Meta     *Meta             ` + "`yaml:\"meta\"`" + `
	Metas    []*Meta           ` + "`yaml:\"metas\"`" + `
	ByName   map[string]Meta   ` + "`yaml:\"by_name\"`" + `
	Note     *string           ` + "`yaml:\"note\"`" + `
	At       *time.Time        ` + "`yaml:\"at\"`" + `
	Timeout  time.Duration     ` + "`yaml:\"timeout\"`" + `
	Inner    struct{ X int }   ` + "`yaml:\"inner\"`" + `
	Ignored  string            ` + "`yaml:\"-\"`" + `
	hidden   string
}

var _ = Thing{}.hidden
`
	data := `- created_at: 2024-01-02T03:04:05.123Z
  id: 1
  level: -3
  ratio: 0.5
  max: .inf
  enabled: true
  tags: [a, "b c"]
  pair: [1, 2]
  labels: {x: 1, y: 2}
  meta: &m {owner: alice}
  metas: [*m, {owner: bob}]
  by_name: {first: *m}
  note: !!str 123
  at: 2024-01-02
  timeout: 1500ms
  inner: {x: 7}
- created_at: 2024-01-02 03:04:05
  id: 4294967295
  level: 0
  ratio: 1
  max: 1e10
  enabled: false
  tags: []
  pair: [0, 0]
  labels: null
  meta: null
  metas: []
  by_name: {}
  inner: {x: 0}
  timeout: 0s
`
	dir := testutil.WriteTree(t, map[string]string{"schema.go": schema, "things.yaml": data})
	path, err := run(MasterData, Config{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	testutil.TypeCheck(t, dir, ".")
	src := testutil.ReadFile(t, path)
	for _, want := range []string{
		"time.Date(2024, time.January, 2, 3, 4, 5, 123000000, time.UTC)",
		"Base: Base{",
		"Max:     math.Inf(1)",
		`Note:    datlasMasterPtr[string]("123")`,
		"1500 * time.Millisecond",
		"case 4294967295:",
		"Metas: []*Meta{ {",
		"func ThingByID(key uint32) (Thing, bool)",
	} {
		if !strings.Contains(strings.Join(strings.Fields(src), " "), strings.Join(strings.Fields(want), " ")) {
			t.Errorf("generated code does not contain %q:\n%s", want, src)
		}
	}
}

func TestRegenerateWithStaleOutput(t *testing.T) {
	dir := testutil.WriteTree(t, map[string]string{
		"schema.go":  roleSchema,
		"roles.yaml": "- code: a\n  name: A\n",
		// Code in the package that depends on the generated identifiers.
		"use.go": "package data\n\nvar _ = Roles\nvar _, _ = RoleByCode(\"a\")\n",
		// A stale generated file that no longer compiles.
		"zz_masterdata_gen.go": "// Code generated by datlas. DO NOT EDIT.\n\npackage data\n\nvar Roles = []Role{{Removed: 1}}\n",
	})
	if _, err := run(MasterData, Config{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	testutil.TypeCheck(t, dir, ".")
}

func TestRefuseToOverwriteHandwrittenFile(t *testing.T) {
	_, err := generate(t, MasterData, map[string]string{
		"schema.go":            roleSchema,
		"roles.yaml":           "[]\n",
		"zz_masterdata_gen.go": "package data\n",
	})
	if err == nil || !strings.Contains(err.Error(), "refusing to modify") {
		t.Fatalf("unexpected error: %v", err)
	}
}

const userSeedSchema = `package seed

//datlas:seed users
type UserSeed struct {
	Email string ` + "`yaml:\"email\" seed:\"key\"`" + `
}

//datlas:seed events
type EventSeed struct {
	Title string ` + "`yaml:\"title\"`" + `
}
`

func TestSeed(t *testing.T) {
	dir := testutil.WriteTree(t, map[string]string{
		"schema.go":         userSeedSchema,
		"local/users.yaml":  "- email: a@example.test\n- email: b@example.test\n",
		"e2e/users.yaml":    "- email: e2e@example.test\n",
		"local/events.yaml": "- title: Launch\n",
	})
	path, err := run(SeedData, Config{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	testutil.TypeCheck(t, dir, ".")
	src := testutil.ReadFile(t, path)
	for _, want := range []string{
		"func LocalUsers() []UserSeed {",
		"func E2eUsers() []UserSeed {",
		"func LocalUserByEmail(key string) (UserSeed, bool)",
		"func LocalEvents() []EventSeed {",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("generated code does not contain %q:\n%s", want, src)
		}
	}
	if strings.Contains(src, "EventByTitle") {
		t.Error("lookup generated for a seed without key")
	}
}

func TestSeedErrors(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{
			name:  "file outside environment",
			files: map[string]string{"users.yaml": "[]\n", "local/events.yaml": "[]\n"},
			want:  "seed data must be placed in an environment directory",
		},
		{
			name:  "unknown dataset",
			files: map[string]string{"local/users.yaml": "[]\n", "local/events.yaml": "[]\n", "local/posts.yaml": "[]\n"},
			want:  `no //datlas:seed type declares dataset "posts"`,
		},
		{
			name:  "duplicate key",
			files: map[string]string{"local/users.yaml": "- email: a\n- email: a\n", "local/events.yaml": "[]\n"},
			want:  "duplicate logical key Email",
		},
		{
			name:  "dataset without file",
			files: map[string]string{"local/users.yaml": "[]\n"},
			want:  `no YAML file found for dataset "events"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.files["schema.go"] = userSeedSchema
			_, err := generate(t, SeedData, tt.files)
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error %q does not contain %q", err, tt.want)
			}
		})
	}
}

func TestDataDir(t *testing.T) {
	dir := testutil.WriteTree(t, map[string]string{
		"schema.go":       roleSchema,
		"data/roles.yaml": "- code: a\n  name: A\n",
	})
	path, err := run(MasterData, Config{Dir: dir, DataDir: "data"})
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(path) != dir {
		t.Fatalf("generated into %s, want %s", path, dir)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}
