package declarative

import (
	"bytes"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/moznion/go-datlas/internal/generator/naming"
	"github.com/moznion/go-datlas/internal/generator/output"
	"github.com/moznion/go-datlas/internal/generator/sqldialect"
)

// hasTables reports whether a dataset is mapped to a table, which generates the Apply function.
func hasTables(datasets []*Dataset) bool {
	for _, ds := range datasets {
		if ds.Table != nil {
			return true
		}
	}
	return false
}

// sqlRenderer renders the functions applying the datasets having a table to a database.
type sqlRenderer struct {
	kind    Kind
	im      *output.Imports
	dialect sqldialect.Dialect
	b       *bytes.Buffer
	ctxPkg  string
	sqlPkg  string
	fmtPkg  string
	jsonPkg string
}

// renderApply renders ApplyMasterData or ApplySeedData. order is the dependency order of the dataset names:
// records are upserted in this order, then stale rows are pruned in the reverse order,
// so that foreign keys between the tables hold.
func renderApply(b *bytes.Buffer, im *output.Imports, kind Kind, datasets []*Dataset, order []string, dialect sqldialect.Dialect) error {
	if !hasTables(datasets) {
		return nil
	}
	r := &sqlRenderer{kind: kind, im: im, dialect: dialect, b: b,
		ctxPkg: im.Add("context", "context"), sqlPkg: im.Add("database/sql", "sql"), fmtPkg: im.Add("fmt", "fmt")}
	p := kind.prefix()
	execIface := fmt.Sprintf("interface {\nExecContext(ctx %s.Context, query string, args ...any) (%s.Result, error)\n}", r.ctxPkg, r.sqlPkg)

	var errs []error
	rows := map[string]bool{}
	for _, ds := range datasets {
		if ds.Table == nil {
			continue
		}
		if !rows[ds.Name] {
			rows[ds.Name] = true
			r.renderRow(ds)
		}
		if err := r.renderDataset(ds); err != nil {
			errs = append(errs, err)
		}
	}
	if err := errors.Join(errs...); err != nil {
		return err
	}

	// steps returns the functions applying the datasets of env in order.
	steps := func(env string) []string {
		byName := map[string]*Dataset{}
		for _, ds := range datasets {
			if ds.Env == env && ds.Table != nil {
				byName[ds.Name] = ds
			}
		}
		var upserts, prunes []string
		for _, name := range order {
			ds := byName[name]
			if ds == nil {
				continue
			}
			upserts = append(upserts, p+"Upsert"+ds.FuncName)
			if r.prunes(ds) {
				prunes = append([]string{p + "Prune" + ds.FuncName}, prunes...)
			}
		}
		return append(upserts, prunes...)
	}
	renderSteps := func(fns []string) {
		fmt.Fprintf(b, "[]func(%s.Context, %sDB) error{\n", r.ctxPkg, p)
		for _, fn := range fns {
			fmt.Fprintf(b, "%s,\n", fn)
		}
		b.WriteString("}")
	}

	fmt.Fprintf(b, "\n// %sDB is the database the %s is applied to.\ntype %sDB = %s\n", p, kind, p, execIface)
	if kind == MasterData {
		fmt.Fprintf(b, `
// %[1]s applies the master data to the database db: it inserts the records of every dataset having
// a table, updating the rows whose keys exist (upsert), in the order of %[2]s, then deletes the rows
// whose keys are not in the master data (prune), in the reverse order.
// db is typically a *sql.DB, *sql.Tx or *sql.Conn; pass a *sql.Tx to apply all or nothing.
func %[1]s(ctx %[3]s.Context, db %[4]s) error {
for _, step := range `, applyMasterName, masterDatasetsName, r.ctxPkg, execIface)
		renderSteps(steps(""))
		b.WriteString(" {\nif err := step(ctx, db); err != nil {\nreturn err\n}\n}\nreturn nil\n}\n")
		return nil
	}

	var envs []string
	seen := map[string]bool{}
	for _, ds := range datasets {
		if !seen[ds.Env] {
			seen[ds.Env] = true
			envs = append(envs, ds.Env)
		}
	}
	fmt.Fprintf(b, `
// %[1]s applies the seed data of the environment env to the database db: it inserts the records
// of every dataset having a table, updating the rows whose keys exist (upsert), in the order of %[2]s,
// then deletes the rows whose keys are not in the seed data of the datasets with prune=true,
// in the reverse order. Records of datasets without a logical key are always inserted.
// db is typically a *sql.DB, *sql.Tx or *sql.Conn; pass a *sql.Tx to apply all or nothing.
func %[1]s(ctx %[3]s.Context, db %[4]s, env string) error {
var steps []func(%[3]s.Context, %[5]sDB) error
switch env {
`, applySeedName, seedDatasetsName, r.ctxPkg, execIface, p)
	for _, env := range sortedStrings(envs) {
		fmt.Fprintf(b, "case %q:\nsteps = ", env)
		renderSteps(steps(env))
		b.WriteString("\n")
	}
	fmt.Fprintf(b, "default:\nreturn %s.Errorf(\"datlas: no seed data for environment %%q\", env)\n}\n", r.fmtPkg)
	b.WriteString("for _, step := range steps {\nif err := step(ctx, db); err != nil {\nreturn err\n}\n}\nreturn nil\n}\n")
	return nil
}

func sortedStrings(xs []string) []string {
	ys := append([]string(nil), xs...)
	sort.Strings(ys)
	return ys
}

// prunes reports whether stale rows of the table of ds are deleted. A seed data environment
// without a file for the dataset leaves its table alone; an empty file deletes every row.
func (r *sqlRenderer) prunes(ds *Dataset) bool {
	return ds.Table.Prune && len(ds.Files) > 0
}

func (r *sqlRenderer) columnNames(ds *Dataset) (cols, keys []string) {
	for _, c := range ds.Table.Columns {
		cols = append(cols, c.Name)
	}
	for _, k := range ds.Table.Keys {
		keys = append(keys, ds.Table.Columns[k].Name)
	}
	return cols, keys
}

// renderRow renders the function returning the column values of a record of the dataset,
// shared by the environments of seed data.
func (r *sqlRenderer) renderRow(ds *Dataset) {
	b, p := r.b, r.kind.prefix()
	typ := r.im.TypeString(ds.Schema.Type.Type())
	fmt.Fprintf(b, "\n// %sRow%s returns the values of the columns of table %s for a record.\n", p, naming.Camel(ds.Name), ds.Table.Name)
	fmt.Fprintf(b, "func %sRow%s(datlasRec %s) ([]any, error) {\n", p, naming.Camel(ds.Name), typ)
	var args []string
	for i, c := range ds.Table.Columns {
		field := "datlasRec." + c.Path
		v := "datlasCol" + strconv.Itoa(i)
		var encode string
		switch {
		case c.Enc == encDirect && c.Ptr && c.Valuer:
			fmt.Fprintf(b, "var %s any\nif %s != nil {\n%s = %s\n}\n", v, field, v, field)
			args = append(args, v)
			continue
		case c.Enc == encDirect:
			args = append(args, field)
			continue
		case c.Enc == encDirectAddr:
			args = append(args, "&"+field)
			continue
		case c.Enc == encText:
			encode = field + ".MarshalText()"
		case c.Enc == encJSON:
			if r.jsonPkg == "" {
				r.jsonPkg = r.im.Add("encoding/json", "json")
			}
			encode = r.jsonPkg + ".Marshal(" + field + ")"
		}
		fail := fmt.Sprintf("return nil, %s.Errorf(\"encode %s: %%w\", err)", r.fmtPkg, c.Path)
		if c.Ptr {
			fmt.Fprintf(b, "var %s any\nif %s != nil {\ndatlasBytes, err := %s\nif err != nil {\n%s\n}\n%s = string(datlasBytes)\n}\n", v, field, encode, fail, v)
			args = append(args, v)
		} else {
			fmt.Fprintf(b, "%s, err := %s\nif err != nil {\n%s\n}\n", v, encode, fail)
			args = append(args, "string("+v+")")
		}
	}
	fmt.Fprintf(b, "return []any{%s}, nil\n}\n", strings.Join(args, ", "))
}

// renderDataset renders the statements of the dataset and the functions executing them.
func (r *sqlRenderer) renderDataset(ds *Dataset) error {
	b, p, d := r.b, r.kind.prefix(), r.dialect
	t := ds.Table
	cols, keys := r.columnNames(ds)
	what := fmt.Sprintf("%s %q", r.kind, ds.Name)
	if ds.Env != "" {
		what += fmt.Sprintf(" (environment %q)", ds.Env)
	}
	if len(cols) > d.MaxParams() {
		return fmt.Errorf("%s: table %s has %d columns, more than the %d parameters a %s statement can take", what, t.Name, len(cols), d.MaxParams(), d.Name())
	}
	if r.prunes(ds) && len(ds.Records)*len(keys) > d.MaxParams() {
		return fmt.Errorf("%s: pruning table %s takes %d parameters, more than the %d a %s statement can take; disable prune", what, t.Name, len(ds.Records)*len(keys), d.MaxParams(), d.Name())
	}
	stmt := d.Insert(t.Name, cols)
	action := "insert into"
	if len(keys) > 0 {
		stmt, action = d.Upsert(t.Name, cols, keys), "upsert into"
	}
	row := p + "Row" + naming.Camel(ds.Name)
	fmt.Fprintf(b, `
// %[1]sUpsert%[2]s applies the records of %[2]s to table %[3]s.
func %[1]sUpsert%[2]s(ctx %[4]s.Context, db %[1]sDB) error {
for i, datlasRec := range %[2]s() {
args, err := %[5]s(datlasRec)
if err == nil {
_, err = db.ExecContext(ctx, %[6]s, args...)
}
if err != nil {
return %[7]s.Errorf("%%s #%%d: %%w", %[8]s, i+1, err)
}
}
return nil
}
`, p, ds.FuncName, t.Name, r.ctxPkg, row, goString(stmt), r.fmtPkg,
		goString(fmt.Sprintf("datlas: %s %s: %s from %s, record", action, t.Name, what, ds.source())))
	if !r.prunes(ds) {
		return nil
	}
	var picks []string
	for _, k := range t.Keys {
		picks = append(picks, "args["+strconv.Itoa(k)+"]")
	}
	fmt.Fprintf(b, `
// %[1]sPrune%[2]s deletes the rows of table %[3]s whose keys are not in %[2]s.
func %[1]sPrune%[2]s(ctx %[4]s.Context, db %[1]sDB) error {
var keys []any
for i, datlasRec := range %[2]s() {
args, err := %[5]s(datlasRec)
if err != nil {
return %[7]s.Errorf("%%s, record #%%d: %%w", %[8]s, i+1, err)
}
keys = append(keys, %[9]s)
}
if _, err := db.ExecContext(ctx, %[6]s, keys...); err != nil {
return %[7]s.Errorf("%%s: %%w", %[8]s, err)
}
return nil
}
`, p, ds.FuncName, t.Name, r.ctxPkg, row, goString(d.DeleteExcept(t.Name, keys, len(ds.Records))), r.fmtPkg,
		goString(fmt.Sprintf("datlas: prune %s: %s from %s", t.Name, what, ds.source())), strings.Join(picks, ", "))
	return nil
}

// goString renders s as a Go string literal, raw when possible so that quoted identifiers stay legible.
func goString(s string) string {
	if strings.ContainsAny(s, "`\r") || !utf8.ValidString(s) {
		return strconv.Quote(s)
	}
	return "`" + s + "`"
}
