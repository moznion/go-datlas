package declarative

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/moznion/go-datlas/internal/generator/naming"
)

// typeName is the name the exported identifiers generated for the diff of k start with.
func (k Kind) typeName() string {
	if k == MasterData {
		return "MasterData"
	}
	return "SeedData"
}

// applyName returns the name of the function applying the datasets of k to a database.
func (k Kind) applyName() string {
	if k == MasterData {
		return applyMasterName
	}
	return applySeedName
}

// diffNames returns the identifiers generated for the diff of k: the function, the change type,
// the operation type and its constants.
func (k Kind) diffNames() (fn, change, op string, ops [4]string) {
	t := k.typeName()
	return "Diff" + t, t + "Change", t + "Op", [4]string{t + "Insert", t + "Update", t + "Unchanged", t + "Delete"}
}

// renderDiff renders DiffMasterData or DiffSeedData, the types of the changes it reports and its helpers.
// steps returns the functions of an environment in the order of the Apply function (see renderApply).
func (r *sqlRenderer) renderDiff(envs []string, steps func(env, upsert, prune string) []string) {
	b, p, kind := r.b, r.kind.prefix(), r.kind
	fn, change, op, ops := kind.diffNames()
	apply := kind.applyName()
	driverPkg, stringsPkg := r.im.Add("database/sql/driver", "driver"), r.im.Add("strings", "strings")
	queryIface := fmt.Sprintf("interface {\nQueryContext(ctx %s.Context, query string, args ...any) (*%s.Rows, error)\n}", r.ctxPkg, r.sqlPkg)

	fmt.Fprintf(b, `
// %[1]s is what %[2]s does to a row.
type %[1]s int

const (
// %[3]s inserts a row for a record.
%[3]s %[1]s = iota + 1
// %[4]s updates the columns of the row having the keys of a record.
%[4]s
// %[5]s finds the row having the keys of a record holding its values already.
%[5]s
// %[6]s deletes a row whose keys are not in the %[7]s (prune).
%[6]s
)

func (op %[1]s) String() string {
switch op {
case %[3]s:
return "insert"
case %[4]s:
return "update"
case %[5]s:
return "unchanged"
case %[6]s:
return "delete"
}
return %[8]s.Sprintf("%[1]s(%%d)", int(op))
}

// %[9]s is what %[2]s does to a row, as reported by %[10]s.
type %[9]s struct {
Op %[1]s
// Table is the table of the row, and Dataset the dataset applied to it.
Table, Dataset string
// Source is the YAML file of the dataset, relative to the data directory.
Source string
// Record is the number, from 1, of the record in Source; 0 for %[6]s.
Record int
// Key is the logical key of the row as text, or nil for a record without a logical key.
Key []string
// Columns are the columns %[4]s changes.
Columns []string
}

// String describes the change, e.g. "update roles (admin): roles.yaml #1: name".
func (c %[9]s) String() string {
s := c.Op.String() + " " + c.Table
if c.Key != nil {
s += " (" + %[11]s.Join(c.Key, ", ") + ")"
}
if c.Record > 0 {
s += %[8]s.Sprintf(": %%s #%%d", c.Source, c.Record)
}
if len(c.Columns) > 0 {
s += ": " + %[11]s.Join(c.Columns, ", ")
}
return s
}

// %[12]sQueryDB is the database %[10]s reads.
type %[12]sQueryDB = %[13]s

// %[12]sKeyText returns a value passed to the database as text, for the key of a change.
func %[12]sKeyText(v any) string {
dv, err := %[14]s.DefaultParameterConverter.ConvertValue(v)
if err != nil {
return %[8]s.Sprint(v)
}
switch dv := dv.(type) {
case nil:
return "NULL"
case []byte:
return string(dv)
}
return %[8]s.Sprint(dv)
}

// %[12]sCompare runs query, which selects whether each of cols is distinct from the value of a record
// in the row having its keys, and returns what applying the record does to the row.
func %[12]sCompare(ctx %[15]s.Context, db %[12]sQueryDB, query string, cols []string, args ...any) (%[1]s, []string, error) {
rows, err := db.QueryContext(ctx, query, args...)
if err != nil {
return 0, nil, err
}
defer rows.Close()
if !rows.Next() {
return %[3]s, nil, rows.Err()
}
var changed []string
if len(cols) > 0 {
distinct := make([]bool, len(cols))
dest := make([]any, len(cols))
for i := range distinct {
dest[i] = &distinct[i]
}
if err := rows.Scan(dest...); err != nil {
return 0, nil, err
}
for i, d := range distinct {
if d {
changed = append(changed, cols[i])
}
}
}
if err := rows.Close(); err != nil {
return 0, nil, err
}
if len(changed) == 0 {
return %[5]s, nil, nil
}
return %[4]s, changed, nil
}

// %[12]sStale runs query, which selects the keys of n columns of the rows prune deletes, as text.
func %[12]sStale(ctx %[15]s.Context, db %[12]sQueryDB, query string, n int, args ...any) ([][]string, error) {
rows, err := db.QueryContext(ctx, query, args...)
if err != nil {
return nil, err
}
defer rows.Close()
var keys [][]string
for rows.Next() {
vals := make([]%[16]s.NullString, n)
dest := make([]any, n)
for i := range vals {
dest[i] = &vals[i]
}
if err := rows.Scan(dest...); err != nil {
return nil, err
}
key := make([]string, n)
for i, v := range vals {
key[i] = "NULL"
if v.Valid {
key[i] = v.String
}
}
keys = append(keys, key)
}
return keys, rows.Err()
}
`, op, apply, ops[0], ops[1], ops[2], ops[3], kind, r.fmtPkg, change, fn, stringsPkg, p, queryIface, driverPkg, r.ctxPkg, r.sqlPkg)

	stepType := fmt.Sprintf("[]func(%s.Context, %sQueryDB) ([]%s, error)", r.ctxPkg, p, change)
	renderSteps := func(fns []string) {
		b.WriteString(stepType + "{\n")
		for _, fn := range fns {
			fmt.Fprintf(b, "%s,\n", fn)
		}
		b.WriteString("}")
	}
	const run = "var changes []%[1]s\nfor _, step := range steps {\ndiff, err := step(ctx, db)\nif err != nil {\nreturn nil, err\n}\nchanges = append(changes, diff...)\n}\nreturn changes, nil\n}\n"
	doc := fmt.Sprintf(`
// %[1]s reports, without writing anything, what %[2]s does to the rows of the database db:
// one change for every record, in the order %[2]s applies them, then one for every row prune deletes.
// A row is %[3]s when the columns the update writes hold the values of the record, compared as
// skip_unchanged=true compares them; without skip_unchanged=true, %[2]s rewrites it all the same
// (and sets its touch column). The rows may change between %[1]s and %[2]s unless both run in
// one transaction isolating them, e.g. at sql.LevelRepeatableRead.
`, fn, apply, ops[2])
	if kind == MasterData {
		fmt.Fprintf(b, "%sfunc %s(ctx %s.Context, db %s) ([]%s, error) {\nsteps := ", doc, fn, r.ctxPkg, queryIface, change)
		renderSteps(steps("", "Diff", "DiffPrune"))
		fmt.Fprintf(b, "\n"+run, change)
		return
	}
	fmt.Fprintf(b, "%sfunc %s(ctx %s.Context, db %s, env string) ([]%s, error) {\nvar steps %s\nswitch env {\n", doc, fn, r.ctxPkg, queryIface, change, stepType)
	for _, env := range envs {
		fmt.Fprintf(b, "case %q:\nsteps = ", env)
		renderSteps(steps(env, "Diff", "DiffPrune"))
		b.WriteString("\n")
	}
	fmt.Fprintf(b, "default:\nreturn nil, %s.Errorf(\"datlas: no seed data for environment %%q\", env)\n}\n", r.fmtPkg)
	fmt.Fprintf(b, run, change)
}

// renderDatasetDiff renders the functions reporting what applying the dataset does to its table,
// for the dataset renderDataset rendered the statements of.
func (r *sqlRenderer) renderDatasetDiff(ds *Dataset) {
	b, p, t := r.b, r.kind.prefix(), ds.Table
	_, change, _, ops := r.kind.diffNames()
	row := p + "Row" + naming.Camel(ds.Name)
	what := fmt.Sprintf("%s %q", r.kind, ds.Name)
	if ds.Env != "" {
		what += fmt.Sprintf(" (environment %q)", ds.Env)
	}
	what += " from " + ds.source()
	base := fmt.Sprintf("Table: %q, Dataset: %q, Source: %q", t.Name, ds.Name, ds.source())

	fmt.Fprintf(b, "\n// %[1]sDiff%[2]s reports what %[1]sUpsert%[2]s does to the rows of table %[3]s.\nfunc %[1]sDiff%[2]s(ctx %[4]s.Context, db %[1]sQueryDB) ([]%[5]s, error) {\nvar changes []%[5]s\nfor i, datlasRec := range %[2]s() {\n",
		p, ds.FuncName, t.Name, r.ctxPkg, change)
	fail := fmt.Sprintf("return nil, %s.Errorf(\"%%s #%%d: %%w\", %s, i+1, err)", r.fmtPkg, goString("datlas: diff "+t.Name+": "+what+", record"))
	if len(t.Keys) == 0 {
		// Records without a logical key are always inserted; encoding them still fails as applying them does.
		fmt.Fprintf(b, "if _, err := %s(datlasRec); err != nil {\n%s\n}\nchanges = append(changes, %s{Op: %s, %s, Record: i + 1})\n}\nreturn changes, nil\n}\n",
			row, fail, change, ops[0], base)
	} else {
		isKey := map[int]bool{}
		var keys, picks, cols []string
		for _, k := range t.Keys {
			isKey[k] = true
			keys = append(keys, t.Columns[k].Name)
			picks = append(picks, "args["+strconv.Itoa(k)+"]")
		}
		keyText := make([]string, len(picks))
		for i, a := range picks {
			keyText[i] = p + "KeyText(" + a + ")"
		}
		// The compared columns are those the update writes: neither keys nor insert-only columns.
		var quoted []string
		for i, c := range t.Columns {
			if !isKey[i] && !c.InsertOnly {
				cols = append(cols, c.Name)
				quoted = append(quoted, strconv.Quote(c.Name))
				picks = append(picks, "args["+strconv.Itoa(i)+"]")
			}
		}
		colsArg := "nil"
		if len(cols) > 0 {
			colsArg = "[]string{" + strings.Join(quoted, ", ") + "}"
		}
		fmt.Fprintf(b, `args, err := %[1]s(datlasRec)
change := %[2]s{%[3]s, Record: i + 1}
if err == nil {
change.Key = []string{%[4]s}
change.Op, change.Columns, err = %[5]sCompare(ctx, db, %[6]s, %[7]s, %[8]s)
}
if err != nil {
%[9]s
}
changes = append(changes, change)
}
return changes, nil
}
`, row, change, base, strings.Join(keyText, ", "), p, goString(r.dialect.Compare(t.Name, keys, cols)), colsArg, strings.Join(picks, ", "), fail)
	}
	if !r.prunes(ds) {
		return
	}
	var picks []string
	var keys []string
	for _, k := range t.Keys {
		picks = append(picks, "args["+strconv.Itoa(k)+"]")
		keys = append(keys, t.Columns[k].Name)
	}
	label := goString("datlas: diff prune " + t.Name + ": " + what)
	fmt.Fprintf(b, `
// %[1]sDiffPrune%[2]s reports the rows of table %[3]s that %[1]sPrune%[2]s deletes.
func %[1]sDiffPrune%[2]s(ctx %[4]s.Context, db %[1]sQueryDB) ([]%[5]s, error) {
var keys []any
for i, datlasRec := range %[2]s() {
args, err := %[6]s(datlasRec)
if err != nil {
return nil, %[7]s.Errorf("%%s, record #%%d: %%w", %[8]s, i+1, err)
}
keys = append(keys, %[9]s)
}
stale, err := %[1]sStale(ctx, db, %[10]s, %[11]d, keys...)
if err != nil {
return nil, %[7]s.Errorf("%%s: %%w", %[8]s, err)
}
var changes []%[5]s
for _, key := range stale {
changes = append(changes, %[5]s{Op: %[12]s, %[13]s, Key: key})
}
return changes, nil
}
`, p, ds.FuncName, t.Name, r.ctxPkg, change, row, r.fmtPkg, label, strings.Join(picks, ", "),
		goString(r.dialect.SelectExcept(t.Name, keys, len(ds.Records))), len(keys), ops[3], base)
}
