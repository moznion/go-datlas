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
	stringsPkg := r.im.Add("strings", "strings")
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

// %[12]sDB is the database %[10]s reads.
type %[12]sDB = %[13]s

// %[12]sQuery returns template, the query comparing the records of a table with its rows, taking the types
// of the columns cols, which typesQuery selects with their names.
func %[12]sQuery(ctx %[15]s.Context, db %[12]sDB, typesQuery, template string, cols ...string) (string, error) {
rows, err := db.QueryContext(ctx, typesQuery)
if err != nil {
return "", err
}
defer rows.Close()
types := map[string]string{}
for rows.Next() {
var name, typ string
if err := rows.Scan(&name, &typ); err != nil {
return "", err
}
types[name] = typ
}
if err := rows.Err(); err != nil {
return "", err
}
slots := make([]any, len(cols))
for i, c := range cols {
typ, ok := types[c]
if !ok {
return "", %[8]s.Errorf("the table has no column %%q", c)
}
slots[i] = typ
}
return %[8]s.Sprintf(template, slots...), nil
}

// %[12]sCompare runs query, which compares a record having n keys with the row having its keys,
// and returns the keys as text and what applying the record does to the row.
func %[12]sCompare(ctx %[15]s.Context, db %[12]sDB, query string, n int, cols []string, args ...any) ([]string, %[1]s, []string, error) {
rows, err := db.QueryContext(ctx, query, args...)
if err != nil {
return nil, 0, nil, err
}
defer rows.Close()
if !rows.Next() {
if err := rows.Err(); err != nil {
return nil, 0, nil, err
}
return nil, 0, nil, %[8]s.Errorf("the comparing query selected no row")
}
keys := make([]%[16]s.NullString, n)
var exists bool
distinct := make([]bool, len(cols))
dest := []any{}
for i := range keys {
dest = append(dest, &keys[i])
}
dest = append(dest, &exists)
for i := range distinct {
dest = append(dest, &distinct[i])
}
if err := rows.Scan(dest...); err != nil {
return nil, 0, nil, err
}
if err := rows.Close(); err != nil {
return nil, 0, nil, err
}
key := make([]string, n)
for i, k := range keys {
key[i] = "NULL"
if k.Valid {
key[i] = k.String
}
}
if !exists {
return key, %[3]s, nil, nil
}
var changed []string
for i, d := range distinct {
if d {
changed = append(changed, cols[i])
}
}
if len(changed) == 0 {
return key, %[5]s, nil, nil
}
return key, %[4]s, changed, nil
}

// %[12]sStale runs query, which selects the keys of n columns of the rows prune deletes, as text.
func %[12]sStale(ctx %[15]s.Context, db %[12]sDB, query string, n int, args ...any) ([][]string, error) {
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
`, op, apply, ops[0], ops[1], ops[2], ops[3], kind, r.fmtPkg, change, fn, stringsPkg, p+"diff", queryIface, "", r.ctxPkg, r.sqlPkg)

	stepType := fmt.Sprintf("[]func(%s.Context, %sdiffDB) ([]%s, error)", r.ctxPkg, p, change)
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
// The values of a record are converted to the types of the columns, as storing them does, and a row is
// %[3]s when the columns the update writes hold them already, compared as skip_unchanged=true
// compares them (json columns as jsonb). Without skip_unchanged=true, %[2]s rewrites such a row
// all the same (and sets its touch column). The rows may change between %[1]s and %[2]s unless
// both run in one transaction isolating them, e.g. at sql.LevelRepeatableRead.
`, fn, apply, ops[2])
	if kind == MasterData {
		fmt.Fprintf(b, "%sfunc %s(ctx %s.Context, db %s) ([]%s, error) {\nsteps := ", doc, fn, r.ctxPkg, queryIface, change)
		renderSteps(steps("", "diffUpsert", "diffPrune"))
		fmt.Fprintf(b, "\n"+run, change)
		return
	}
	fmt.Fprintf(b, "%sfunc %s(ctx %s.Context, db %s, env string) ([]%s, error) {\nvar steps %s\nswitch env {\n", doc, fn, r.ctxPkg, queryIface, change, stepType)
	for _, env := range envs {
		fmt.Fprintf(b, "case %q:\nsteps = ", env)
		renderSteps(steps(env, "diffUpsert", "diffPrune"))
		b.WriteString("\n")
	}
	fmt.Fprintf(b, "default:\nreturn nil, %s.Errorf(\"datlas: no seed data for environment %%q\", env)\n}\n", r.fmtPkg)
	fmt.Fprintf(b, run, change)
}

// renderDatasetDiff renders the functions reporting what applying the dataset does to its table,
// for the dataset renderDataset rendered the statements of. Their names start with the prefix and
// "diff": the names derived from datasets continue the prefix with an upper-case letter, so they never
// collide with them.
func (r *sqlRenderer) renderDatasetDiff(ds *Dataset) {
	b, p, t := r.b, r.kind.prefix(), ds.Table
	h := p + "diff"
	_, change, _, ops := r.kind.diffNames()
	row := p + "Row" + naming.Camel(ds.Name)
	what := fmt.Sprintf("%s %q", r.kind, ds.Name)
	if ds.Env != "" {
		what += fmt.Sprintf(" (environment %q)", ds.Env)
	}
	what += " from " + ds.source()
	base := fmt.Sprintf("Table: %q, Dataset: %q, Source: %q", t.Name, ds.Name, ds.source())

	fmt.Fprintf(b, "\n// %[1]sUpsert%[2]s reports what %[3]sUpsert%[2]s does to the rows of table %[4]s.\nfunc %[1]sUpsert%[2]s(ctx %[5]s.Context, db %[1]sDB) ([]%[6]s, error) {\n",
		h, ds.FuncName, p, t.Name, r.ctxPkg, change)
	label := goString("datlas: diff " + t.Name + ": " + what)
	if len(t.Keys) == 0 {
		// Records without a logical key are always inserted; encoding them still fails as applying them does.
		fmt.Fprintf(b, `var changes []%[1]s
for i, datlasRec := range %[2]s() {
if _, err := %[3]s(datlasRec); err != nil {
return nil, %[4]s.Errorf("%%s, record #%%d: %%w", %[5]s, i+1, err)
}
changes = append(changes, %[1]s{Op: %[6]s, %[7]s, Record: i + 1})
}
return changes, nil
}
`, change, ds.FuncName, row, r.fmtPkg, label, ops[0], base)
	} else {
		isKey := map[int]bool{}
		var keys, cols, picks, quoted []string
		for _, k := range t.Keys {
			isKey[k] = true
			keys = append(keys, t.Columns[k].Name)
			picks = append(picks, "args["+strconv.Itoa(k)+"]")
		}
		// The compared columns are those the update writes: neither keys nor insert-only columns.
		for i, c := range t.Columns {
			if !isKey[i] && !c.InsertOnly {
				cols = append(cols, c.Name)
				picks = append(picks, "args["+strconv.Itoa(i)+"]")
			}
		}
		for _, c := range append(append([]string(nil), keys...), cols...) {
			quoted = append(quoted, strconv.Quote(c))
		}
		colsArg := "nil"
		if len(cols) > 0 {
			colsArg = "[]string{" + strings.Join(quoted[len(keys):], ", ") + "}"
		}
		fmt.Fprintf(b, `records := %[1]s()
if len(records) == 0 {
return nil, nil
}
query, err := %[2]sQuery(ctx, db, %[3]s, %[4]s, %[5]s)
if err != nil {
return nil, %[6]s.Errorf("%%s: %%w", %[7]s, err)
}
var changes []%[8]s
for i, datlasRec := range records {
args, err := %[9]s(datlasRec)
change := %[8]s{%[10]s, Record: i + 1}
if err == nil {
change.Key, change.Op, change.Columns, err = %[2]sCompare(ctx, db, query, %[11]d, %[12]s, %[13]s)
}
if err != nil {
return nil, %[6]s.Errorf("%%s, record #%%d: %%w", %[7]s, i+1, err)
}
changes = append(changes, change)
}
return changes, nil
}
`, ds.FuncName, h, goString(r.dialect.ColumnTypes(t.Name)), goString(r.dialect.Compare(t.Name, keys, cols)), strings.Join(quoted, ", "),
			r.fmtPkg, label, change, row, base, len(keys), colsArg, strings.Join(picks, ", "))
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
	label = goString("datlas: diff prune " + t.Name + ": " + what)
	fmt.Fprintf(b, `
// %[1]sPrune%[2]s reports the rows of table %[3]s that %[14]sPrune%[2]s deletes.
func %[1]sPrune%[2]s(ctx %[4]s.Context, db %[1]sDB) ([]%[5]s, error) {
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
`, h, ds.FuncName, t.Name, r.ctxPkg, change, row, r.fmtPkg, label, strings.Join(picks, ", "),
		goString(r.dialect.SelectExcept(t.Name, keys, len(ds.Records))), len(keys), ops[3], base, p)
}
