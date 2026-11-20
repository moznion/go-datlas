package declarative

import (
	"testing"

	"github.com/moznion/go-datlas/internal/generator/testutil"
)

// These tests run the generated Apply functions against a recording executor in a temporary module.

const applyRunSchema = `package data

import (
	"database/sql/driver"
	"errors"
	"strings"
)

// FailText and FailJSON make encoding fail, to test the errors of the generated code.
var FailText, FailJSON bool

type Code string

func (c *Code) UnmarshalText(b []byte) error { *c = Code(strings.ToLower(string(b))); return nil }
func (c Code) MarshalText() ([]byte, error) {
	if FailText {
		return nil, errors.New("bad code")
	}
	return []byte(c), nil
}

// VCode implements both driver.Valuer and encoding.TextUnmarshaler: it is passed as is.
type VCode string

func (c *VCode) UnmarshalText(b []byte) error  { *c = VCode(b); return nil }
func (c VCode) Value() (driver.Value, error)    { return "v:" + string(c), nil }

// Money implements driver.Valuer with a pointer receiver: its address is passed.
type Money struct {
	Yen int ` + "`yaml:\"yen\"`" + `
}

func (m *Money) Value() (driver.Value, error) { return int64(m.Yen), nil }

type Meta struct {
	A int ` + "`yaml:\"a\" json:\"a\"`" + `
}

type Boom struct {
	X int ` + "`yaml:\"x\"`" + `
}

func (b Boom) MarshalJSON() ([]byte, error) {
	if FailJSON {
		return nil, errors.New("boom")
	}
	return []byte("\"ok\""), nil
}

//datlas:master items table=items
type Item struct {
	ID    Code            ` + "`yaml:\"id\" master:\"key\"`" + `
	Alias *Code           ` + "`yaml:\"alias\"`" + `
	V     VCode           ` + "`yaml:\"v\"`" + `
	Price Money           ` + "`yaml:\"price\"`" + `
	PM    *Money          ` + "`yaml:\"pm\"`" + `
	Meta  *Meta           ` + "`yaml:\"meta\"`" + `
	Attrs *map[string]int ` + "`yaml:\"attrs\"`" + `
	Note  *string         ` + "`yaml:\"note\"`" + `
	Boom  Boom            ` + "`yaml:\"boom\"`" + `
}

//datlas:seed events table=events
type Event struct {
	Kind string ` + "`yaml:\"kind\"`" + `
}
`

const applyRunTest = `package data_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"reflect"
	"strings"
	"testing"

	"example.com/datlastest/data"
)

type call struct {
	query string
	args  []any
}

type recorder struct {
	calls  []call
	failOn string
}

var errExec = errors.New("exec failed")

func (r *recorder) ExecContext(_ context.Context, query string, args ...any) (sql.Result, error) {
	r.calls = append(r.calls, call{query, args})
	if r.failOn != "" && strings.HasPrefix(query, r.failOn) {
		return nil, errExec
	}
	return nil, nil
}

// driverValues converts args as database/sql does, failing for values a driver cannot take.
func driverValues(t *testing.T, args []any) []any {
	t.Helper()
	var vs []any
	for _, a := range args {
		if v, ok := a.(driver.Valuer); ok {
			var err error
			if a, err = v.Value(); err != nil {
				t.Fatal(err)
			}
		}
		v, err := driver.DefaultParameterConverter.ConvertValue(a)
		if err != nil {
			t.Fatalf("%#v cannot be passed to a driver: %v", a, err)
		}
		vs = append(vs, v)
	}
	return vs
}

func TestValues(t *testing.T) {
	var r recorder
	if err := data.ApplyMasterData(context.Background(), &r); err != nil {
		t.Fatal(err)
	}
	if len(r.calls) != 3 {
		t.Fatalf("calls: %+v", r.calls)
	}
	if _, ok := r.calls[0].args[3].(*data.Money); !ok {
		t.Errorf("Money is passed as %T, not by address", r.calls[0].args[3])
	}
	if _, ok := r.calls[0].args[4].(*data.Money); !ok {
		t.Errorf("*Money is passed as %T, not as is", r.calls[0].args[4])
	}
	if r.calls[1].args[4] != nil {
		t.Errorf("nil *Money is passed as %#v, not as nil", r.calls[1].args[4])
	}
	if _, ok := r.calls[0].args[2].(data.VCode); !ok {
		t.Errorf("VCode is passed as %T, not as is", r.calls[0].args[2])
	}
	want := [][]any{
		{"a", "x", "v:q", int64(5), int64(9), "{\"a\":1}", "{\"k\":2}", "n", "\"ok\""},
		{"b", nil, "v:r", int64(7), nil, nil, nil, nil, "\"ok\""},
	}
	for i, w := range want {
		if got := driverValues(t, r.calls[i].args); !reflect.DeepEqual(got, w) {
			t.Errorf("record %d: got %#v, want %#v", i+1, got, w)
		}
	}
	prune := r.calls[2]
	if prune.query != "DELETE FROM \"items\" WHERE \"id\" NOT IN ($1, $2)" || !reflect.DeepEqual(driverValues(t, prune.args), []any{"a", "b"}) {
		t.Errorf("prune: %+v", prune)
	}
}

func TestErrors(t *testing.T) {
	const upsert = "datlas: upsert into items: master data \"items\" from items.yaml, record #1: "
	tests := []struct {
		name      string
		failOn    string
		failText  bool
		failJSON  bool
		want      string
		wantExec  bool
	}{
		{name: "exec upsert", failOn: "INSERT", want: upsert + "exec failed", wantExec: true},
		{name: "exec prune", failOn: "DELETE", want: "datlas: prune items: master data \"items\" from items.yaml: exec failed", wantExec: true},
		{name: "MarshalText", failText: true, want: upsert + "encode ID: bad code"},
		{name: "json.Marshal", failJSON: true, want: upsert + "encode Boom: json: error calling MarshalJSON for type *data.Boom: boom"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data.FailText, data.FailJSON = tt.failText, tt.failJSON
			defer func() { data.FailText, data.FailJSON = false, false }()
			r := recorder{failOn: tt.failOn}
			err := data.ApplyMasterData(context.Background(), &r)
			if err == nil || err.Error() != tt.want {
				t.Fatalf("err = %v\nwant  %s", err, tt.want)
			}
			if errors.Is(err, errExec) != tt.wantExec {
				t.Errorf("errors.Is(err, errExec) = %v", !tt.wantExec)
			}
		})
	}
}

func TestSeedErrors(t *testing.T) {
	r := recorder{failOn: "INSERT"}
	err := data.ApplySeedData(context.Background(), &r, "local")
	want := "datlas: insert into events: seed data \"events\" (environment \"local\") from local/events.yaml, record #1: exec failed"
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v\nwant  %s", err, want)
	}
	if err := data.ApplySeedData(context.Background(), &recorder{}, "prod"); err == nil || err.Error() != "datlas: no seed data for environment \"prod\"" {
		t.Fatalf("unknown env: %v", err)
	}
}
`

func TestApplyRun(t *testing.T) {
	dir := testutil.WriteTree(t, map[string]string{
		"data/schema.go":           applyRunSchema,
		"data/run_test.go":         applyRunTest,
		"data/m/items.yaml":        "- {id: A, alias: X, v: q, price: {yen: 5}, pm: {yen: 9}, meta: {a: 1}, attrs: {k: 2}, note: n, boom: {x: 1}}\n- {id: b, v: r, price: {yen: 7}, boom: {x: 2}}\n",
		"data/s/local/events.yaml": "- {kind: k}\n",
	})
	if _, err := run(MasterData, Config{Dir: dir, Package: "./data", DataDir: "m"}); err != nil {
		t.Fatal(err)
	}
	if _, err := run(SeedData, Config{Dir: dir, Package: "./data", DataDir: "s"}); err != nil {
		t.Fatal(err)
	}
	testutil.GoTest(t, dir, "./data")
}

// TestApplyNameCollisions declares, in the schema package, the names of the packages the generated
// code imports and of its local variables, which must neither break nor be broken by it.
func TestApplyNameCollisions(t *testing.T) {
	schema := `package data

type sql struct{}

var context = 1

func fmt() {}

type json int

var (
	ctx, db, i, err, args, keys, step, steps, datlasRec, datlasCol1, datlasBytes int
)

var _, _, _ = sql{}, context, json(0)

//datlas:master keys table=keys
type KeyRow struct {
	K string            ` + "`yaml:\"k\" master:\"key\"`" + `
	M map[string]int    ` + "`yaml:\"m\"`" + `
	P *map[string]int   ` + "`yaml:\"p\"`" + `
}
`
	test := `package data_test

import (
	"context"
	"database/sql"
	"testing"

	"example.com/datlastest/data"
)

type recorder struct{ n int }

func (r *recorder) ExecContext(context.Context, string, ...any) (sql.Result, error) { r.n++; return nil, nil }

func TestApply(t *testing.T) {
	var r recorder
	if err := data.ApplyMasterData(context.Background(), &r); err != nil || r.n != 2 {
		t.Fatalf("err = %v, %d statements", err, r.n)
	}
}
`
	dir := testutil.WriteTree(t, map[string]string{
		"data/schema.go":   schema,
		"data/run_test.go": test,
		"data/keys.yaml":   "- {k: a, m: {x: 1}, p: {y: 2}}\n",
	})
	path, err := run(MasterData, Config{Dir: dir, Package: "./data"})
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, testutil.ReadFile(t, path), `context2 "context"`, `sql2 "database/sql"`, `fmt2 "fmt"`, `json2 "encoding/json"`)
	testutil.GoTest(t, dir, "./data")
}
