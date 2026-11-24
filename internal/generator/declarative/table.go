package declarative

import (
	"errors"
	"fmt"
	"go/types"
	"reflect"
	"strings"

	"github.com/moznion/go-datlas/internal/generator/loader"
	"github.com/moznion/go-datlas/internal/generator/sqldialect"
)

// DBTagKey is the struct tag key naming the column of a field, e.g. `db:"price"`, or excluding it (`db:"-"`).
// The option insertonly (`db:"label,insertonly"`, or `db:",insertonly"` keeping the default name)
// writes the column when a row is inserted, not when it is updated.
const DBTagKey = "db"

// dbTagInsertOnly is the option of the db tag writing a column on insert only.
const dbTagInsertOnly = "insertonly"

// Table maps a dataset to a database table (`table=<name>` in the directive).
type Table struct {
	Name string
	// Prune deletes the rows whose keys are not in the dataset after applying it.
	Prune bool
	// Touch is a column, not filled from the records, set to the current time when a row is updated
	// (touch=<column>), or empty.
	Touch string
	// SkipUnchanged leaves the rows whose columns already hold the values of the record (skip_unchanged=true).
	SkipUnchanged bool
	// Columns are the columns written for each record, in field order.
	Columns []Column
	// Keys are the indexes in Columns of the logical key, in the order of Schema.Keys.
	Keys []int
}

// columnEncoding is how the value of a field is passed to the database.
type columnEncoding int

const (
	// encDirect passes the field as is: database/sql converts it (basic types, time.Time,
	// []byte, driver.Valuer implementations and pointers to them; nil pointers are NULL).
	encDirect columnEncoding = iota
	// encDirectAddr passes the address of the field, whose pointer type implements driver.Valuer.
	encDirectAddr
	// encText passes the text of encoding.TextMarshaler as a string.
	encText
	// encJSON passes the JSON encoding of the field as a string.
	encJSON
)

// Column is a column of a table and the field its values come from.
type Column struct {
	Name string
	// Field is the field of the schema (or of an inline struct) the column is filled from.
	Field *types.Var
	// Path is the selector of the field from the record, e.g. "Limits" or "Base.CreatedAt" for inline structs.
	Path string
	Type types.Type
	Enc  columnEncoding
	// Ptr is true when the field is a pointer, whose nil value is passed as NULL.
	Ptr bool
	// Valuer is true when the field implements driver.Valuer. A nil pointer is then passed as an
	// untyped nil: database/sql would call a Value method with a pointer receiver on it.
	Valuer bool
	// InsertOnly is true when the column is written on insert only (`db:"<column>,insertonly"`).
	InsertOnly bool
}

// tableOptions are the options of the directive mapping a dataset to a table.
type tableOptions struct {
	name          string
	prune         bool
	touch         string
	skipUnchanged bool
}

// dbTag is a parsed `db` tag.
type dbTag struct {
	// name is the column name, or empty for the default.
	name string
	// skip excludes the field (`db:"-"`).
	skip       bool
	insertOnly bool
}

// parseDBTag parses the `db` tag of a field.
func parseDBTag(tag string) (dbTag, error) {
	v, ok := reflect.StructTag(tag).Lookup(DBTagKey)
	if !ok {
		return dbTag{}, nil
	}
	if v == "-" {
		return dbTag{skip: true}, nil
	}
	name, opts, hasOpts := strings.Cut(v, ",")
	t := dbTag{name: name}
	if !hasOpts {
		return t, nil
	}
	if name == "-" {
		return dbTag{}, fmt.Errorf("%q excludes the field, which takes no options; use %s:\"-\" alone", v, DBTagKey)
	}
	for _, o := range strings.Split(opts, ",") {
		switch {
		case o == "":
			// A trailing or doubled comma is likely an option left out, e.g. `db:"label,"`.
			return dbTag{}, fmt.Errorf("empty %s tag option in %q (available: %s)", DBTagKey, v, dbTagInsertOnly)
		case o != dbTagInsertOnly:
			return dbTag{}, fmt.Errorf("unknown %s tag option %q (available: %s)", DBTagKey, o, dbTagInsertOnly)
		case t.insertOnly:
			return dbTag{}, fmt.Errorf("repeated %s tag option %q", DBTagKey, o)
		}
		t.insertOnly = true
	}
	return t, nil
}

// tableOf maps the schema to the columns of table. Inline structs are flattened;
// every other field is one column, named by its `db` tag or else by its YAML key.
func tableOf(kind Kind, d loader.Directive, schema Schema, opts tableOptions, enums *loader.Enums, dialect sqldialect.Dialect) (*Table, error) {
	ctx := fmt.Sprintf("%s: %s", d.Pos, d.Type.Name())
	table, prune := opts.name, opts.prune
	if !sqldialect.ValidTable(table) {
		return nil, fmt.Errorf("%s: invalid table name %q (an identifier of letters, digits and '_', optionally qualified by a schema: schema.table)", ctx, table)
	}
	for _, part := range strings.Split(table, ".") {
		if err := dialect.CheckIdentifier(part); err != nil {
			return nil, fmt.Errorf("%s: table name: %w", ctx, err)
		}
	}
	if prune && len(schema.Keys) == 0 {
		return nil, fmt.Errorf("%s: prune=true requires a logical key (`%s:\"key\"`) to tell the records of the dataset", ctx, kind.tagKey())
	}
	if len(schema.Keys) == 0 {
		if opts.touch != "" {
			return nil, fmt.Errorf("%s: touch=%s requires a logical key (`%s:\"key\"`): records without one are inserted, never updated", ctx, opts.touch, kind.tagKey())
		}
		if opts.skipUnchanged {
			return nil, fmt.Errorf("%s: skip_unchanged=true requires a logical key (`%s:\"key\"`): records without one are inserted, never updated", ctx, kind.tagKey())
		}
	}
	if opts.touch != "" {
		if !sqldialect.ValidIdentifier(opts.touch) {
			return nil, fmt.Errorf("%s: invalid touch column name %q (letters, digits and '_')", ctx, opts.touch)
		}
		if err := dialect.CheckIdentifier(opts.touch); err != nil {
			return nil, fmt.Errorf("%s: touch column name: %w", ctx, err)
		}
	}
	t := &Table{Name: table, Prune: prune, Touch: opts.touch, SkipUnchanged: opts.skipUnchanged}
	var (
		errs   []error
		byName = map[string]string{}
		byVar  = map[*types.Var]int{}
	)
	var walk func(st *types.Struct, path string)
	walk = func(st *types.Struct, path string) {
		for _, f := range yamlFields(st) {
			fpath := path + f.v.Name()
			fctx := fmt.Sprintf("%s.%s", ctx, fpath)
			tag, err := parseDBTag(f.rawTag)
			switch {
			case err != nil:
				errs = append(errs, fmt.Errorf("%s: %w", fctx, err))
				continue
			case tag.skip:
				continue
			}
			name := tag.name
			if f.tag.inline {
				if tag.insertOnly {
					errs = append(errs, fmt.Errorf("%s: %s cannot be set on an inline struct, which has no column of its own; set it on its fields instead", fctx, dbTagInsertOnly))
					continue
				}
				if name != "" {
					errs = append(errs, fmt.Errorf("%s: an inline struct has no column of its own; tag its fields instead", fctx))
					continue
				}
				walk(types.Unalias(f.v.Type()).Underlying().(*types.Struct), fpath+".")
				continue
			}
			if name == "" {
				name = f.yamlName
			}
			if !sqldialect.ValidIdentifier(name) {
				errs = append(errs, fmt.Errorf("%s: invalid column name %q (letters, digits and '_'); name the column with a `%s:\"<column>\"` tag", fctx, name, DBTagKey))
				continue
			}
			if err := dialect.CheckIdentifier(name); err != nil {
				errs = append(errs, fmt.Errorf("%s: column name: %w; name the column with a `%s:\"<column>\"` tag", fctx, err, DBTagKey))
				continue
			}
			if prev, dup := byName[name]; dup {
				errs = append(errs, fmt.Errorf("%s: column %q is also used by %s", fctx, name, prev))
				continue
			}
			if name == opts.touch {
				errs = append(errs, fmt.Errorf("%s: column %q is the touch column, which is set to the current time rather than written from the records", fctx, name))
				continue
			}
			byName[name] = fpath
			col, err := columnOf(name, fpath, f.v, enums)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", fctx, err))
				continue
			}
			if tag.insertOnly && len(schema.Keys) == 0 {
				errs = append(errs, fmt.Errorf("%s: %s requires a logical key (`%s:\"key\"`): records without one are inserted, never updated", fctx, dbTagInsertOnly, kind.tagKey()))
				continue
			}
			col.InsertOnly = tag.insertOnly
			byVar[f.v] = len(t.Columns)
			t.Columns = append(t.Columns, col)
		}
	}
	walk(schema.Struct, "")
	for _, k := range schema.Keys {
		i, ok := byVar[k]
		if !ok {
			errs = append(errs, fmt.Errorf("%s.%s: a logical key field cannot be excluded from the table", ctx, k.Name()))
			continue
		}
		if t.Columns[i].InsertOnly {
			errs = append(errs, fmt.Errorf("%s.%s: a logical key field cannot be %s: the keys are never updated", ctx, k.Name(), dbTagInsertOnly))
			continue
		}
		t.Keys = append(t.Keys, i)
	}
	if len(t.Columns) == 0 && len(errs) == 0 {
		errs = append(errs, fmt.Errorf("%s: table %s has no columns", ctx, table))
	}
	if opts.touch != "" && len(errs) == 0 && !t.updates() {
		errs = append(errs, fmt.Errorf("%s: touch=%s has no effect: every column but the keys is %s or there is none, so existing rows are never updated", ctx, opts.touch, dbTagInsertOnly))
	}
	return t, errors.Join(errs...)
}

// updates reports whether the upsert of the table updates a column of existing rows:
// a column that is neither a key nor insert-only.
func (t *Table) updates() bool {
	isKey := map[int]bool{}
	for _, k := range t.Keys {
		isKey[k] = true
	}
	for i, c := range t.Columns {
		if !isKey[i] && !c.InsertOnly {
			return true
		}
	}
	return false
}

// columnOf decides how the values of a field of type t are passed to the database.
func columnOf(name, path string, v *types.Var, enums *loader.Enums) (Column, error) {
	t := v.Type()
	col := Column{Name: name, Field: v, Path: path, Type: t}
	elem := types.Unalias(t)
	if p, ok := elem.(*types.Pointer); ok {
		col.Ptr = true
		elem = types.Unalias(p.Elem())
	}
	switch {
	case hasValuer(t):
		// For a pointer field, *T implements driver.Valuer when T does or when its method has a
		// pointer receiver.
		col.Enc, col.Valuer = encDirect, true
	case !col.Ptr && hasValuer(types.NewPointer(elem)):
		col.Enc = encDirectAddr
	case isText(elem, enums):
		if !hasTextMarshaler(types.NewPointer(elem)) {
			return col, fmt.Errorf("%s implements encoding.TextUnmarshaler but neither driver.Valuer nor encoding.TextMarshaler, so its values cannot be written to the database; implement one of them, or exclude the field with `%s:\"-\"`", typeName(elem), DBTagKey)
		}
		col.Enc = encText
	case isTime(elem) || isDuration(elem) || isBytes(elem):
		col.Enc = encDirect
	default:
		if _, ok := elem.Underlying().(*types.Basic); ok {
			col.Enc = encDirect
			break
		}
		if err := (&jsonChecker{seen: map[types.Type]bool{}}).check(t); err != nil {
			return col, fmt.Errorf("the column is written as JSON, but %w; exclude the field with `%s:\"-\"`", err, DBTagKey)
		}
		col.Enc = encJSON
	}
	return col, nil
}

// jsonChecker finds the values encoding/json cannot encode, among those YAML can hold.
type jsonChecker struct{ seen map[types.Type]bool }

func (jc *jsonChecker) check(t types.Type) error {
	t = types.Unalias(t)
	if jc.seen[t] || hasJSONMarshaler(t) || hasJSONMarshaler(types.NewPointer(t)) {
		return nil
	}
	jc.seen[t] = true
	switch u := t.Underlying().(type) {
	case *types.Pointer:
		return jc.check(u.Elem())
	case *types.Slice:
		return jc.check(u.Elem())
	case *types.Array:
		return jc.check(u.Elem())
	case *types.Map:
		if b, ok := types.Unalias(u.Key()).Underlying().(*types.Basic); ok && b.Info()&types.IsBoolean != 0 && !hasTextMarshaler(u.Key()) {
			return fmt.Errorf("encoding/json cannot encode maps with keys of type %s", typeName(u.Key()))
		}
		return jc.check(u.Elem())
	case *types.Struct:
		for i := range u.NumFields() {
			f := u.Field(i)
			// encoding/json encodes the exported fields of embedded structs, even unexported ones.
			if !f.Exported() && !f.Embedded() || reflect.StructTag(u.Tag(i)).Get("json") == "-" {
				continue
			}
			if err := jc.check(f.Type()); err != nil {
				return err
			}
		}
	}
	return nil
}

// hasJSONMarshaler reports whether t implements json.Marshaler, which decides how its values are encoded.
func hasJSONMarshaler(t types.Type) bool {
	return hasMethod(t, "MarshalJSON", func(res *types.Tuple) bool { return isBytes(res.At(0).Type()) })
}

// jsonColumns returns the fields written as JSON, by the name of their column.
func (t *Table) jsonColumns() map[*types.Var]string {
	m := map[*types.Var]string{}
	for _, c := range t.Columns {
		if c.Enc == encJSON {
			m[c.Field] = c.Name
		}
	}
	return m
}

// hasValuer reports whether t implements database/sql/driver.Valuer.
func hasValuer(t types.Type) bool {
	return hasMethod(t, "Value", func(res *types.Tuple) bool {
		return isNamed(res.At(0).Type(), "database/sql/driver", "Value")
	})
}

// hasTextMarshaler reports whether t implements encoding.TextMarshaler.
func hasTextMarshaler(t types.Type) bool {
	return hasMethod(t, "MarshalText", func(res *types.Tuple) bool { return isBytes(res.At(0).Type()) })
}

// hasMethod reports whether t has a method name() (X, error) whose result X satisfies first.
func hasMethod(t types.Type, name string, first func(*types.Tuple) bool) bool {
	sel := types.NewMethodSet(t).Lookup(nil, name)
	if sel == nil {
		return false
	}
	sig := sel.Obj().Type().(*types.Signature)
	res := sig.Results()
	return sig.Params().Len() == 0 && res.Len() == 2 && first(res) &&
		types.Identical(res.At(1).Type(), types.Universe.Lookup("error").Type())
}
