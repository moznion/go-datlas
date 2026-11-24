// Package sqldialect renders the SQL statements that apply declarative data to a database.
// Each supported RDBMS implements Dialect; the generator only depends on that interface.
package sqldialect

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Dialect renders the statements of one RDBMS. Table names may be qualified (schema.table);
// table and column names are valid identifiers (see ValidIdentifier). Statements take
// their values as positional parameters, in the order of the columns (or keys) given.
type Dialect interface {
	// Name is the name selecting the dialect, e.g. "postgres".
	Name() string
	// Insert inserts one row.
	Insert(table string, cols []string) string
	// Upsert inserts one row, or updates the row having the same keys.
	Upsert(u UpsertSpec) string
	// DeleteExcept deletes the rows whose keys are none of the given rows of keys.
	// It takes rows*len(keys) parameters, row by row; with no rows it deletes every row.
	DeleteExcept(table string, keys []string, rows int) string
	// MaxParams is the largest number of parameters a statement can take.
	MaxParams() int
	// CheckIdentifier reports why name, a valid identifier (see ValidIdentifier) of a table,
	// schema or column, is a mistake in this dialect, or returns nil.
	CheckIdentifier(name string) error
}

// UpsertSpec describes an upsert statement.
type UpsertSpec struct {
	Table string
	// Cols are the columns inserted, in the order of the parameters.
	Cols []string
	// Keys are the columns of Cols identifying a row, which the update leaves alone.
	Keys []string
	// InsertOnly are the columns of Cols written by the insert only: the update leaves them alone.
	InsertOnly []string
	// Touch is a column, not in Cols, that the update sets to the current time, or empty.
	// It is ignored when no column is updated (Updated is empty), since nothing is then updated:
	// callers wanting the column to move must reject that case.
	Touch string
}

// Updated returns the columns the update writes from the parameters: Cols without Keys and InsertOnly.
func (u UpsertSpec) Updated() []string {
	skip := map[string]bool{}
	for _, c := range u.Keys {
		skip[c] = true
	}
	for _, c := range u.InsertOnly {
		skip[c] = true
	}
	var cols []string
	for _, c := range u.Cols {
		if !skip[c] {
			cols = append(cols, c)
		}
	}
	return cols
}

var dialects = map[string]Dialect{
	"postgres": postgres{},
}

// Default is the name of the dialect used when none is given.
const Default = "postgres"

// Lookup returns the dialect named name.
func Lookup(name string) (Dialect, error) {
	if d, ok := dialects[name]; ok {
		return d, nil
	}
	return nil, fmt.Errorf("unknown SQL dialect %q (available: %s)", name, strings.Join(Names(), ", "))
}

// Names returns the names of the supported dialects in alphabetical order.
func Names() []string {
	var names []string
	for n := range dialects {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// ValidIdentifier reports whether name is a letter or '_' followed by letters, digits and '_'.
// Such names are portable across dialects; they are still quoted in statements.
func ValidIdentifier(name string) bool {
	for i, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '_':
		case i > 0 && r >= '0' && r <= '9':
		default:
			return false
		}
	}
	return name != ""
}

// ValidTable reports whether name is an identifier, optionally qualified by a schema (schema.table).
func ValidTable(name string) bool {
	parts := strings.Split(name, ".")
	if len(parts) > 2 {
		return false
	}
	for _, p := range parts {
		if !ValidIdentifier(p) {
			return false
		}
	}
	return true
}

// postgres is PostgreSQL: double-quoted identifiers, $n parameters and ON CONFLICT.
type postgres struct{}

func (postgres) Name() string { return "postgres" }

// MaxParams is the limit of the PostgreSQL wire protocol, which counts parameters in 16 bits.
func (postgres) MaxParams() int { return 65535 }

// CheckIdentifier rejects upper-case letters. Statements quote identifiers, which makes them
// case-sensitive, while PostgreSQL folds the unquoted identifiers of DDL to lower case:
// "Users" would not find a table created with CREATE TABLE Users.
func (postgres) CheckIdentifier(name string) error {
	if strings.ToLower(name) != name {
		return fmt.Errorf("%q has upper-case letters, which PostgreSQL folds to lower case unless quoted; use %q", name, strings.ToLower(name))
	}
	return nil
}

func (postgres) quote(name string) string {
	parts := strings.Split(name, ".")
	for i, p := range parts {
		parts[i] = `"` + strings.ReplaceAll(p, `"`, `""`) + `"`
	}
	return strings.Join(parts, ".")
}

func (d postgres) quoteAll(names []string) string {
	qs := make([]string, len(names))
	for i, n := range names {
		qs[i] = d.quote(n)
	}
	return strings.Join(qs, ", ")
}

// params returns "$from, ..., $(from+n-1)".
func (postgres) params(from, n int) string {
	ps := make([]string, n)
	for i := range ps {
		ps[i] = "$" + strconv.Itoa(from+i)
	}
	return strings.Join(ps, ", ")
}

func (d postgres) Insert(table string, cols []string) string {
	return fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)", d.quote(table), d.quoteAll(cols), d.params(1, len(cols)))
}

// Upsert updates the row with the same keys through ON CONFLICT, whose target must match a unique
// constraint or index without a predicate. When no column is updated, it does nothing.
func (d postgres) Upsert(u UpsertSpec) string {
	updated := u.Updated()
	stmt := fmt.Sprintf("%s ON CONFLICT (%s) ", d.Insert(u.Table, u.Cols), d.quoteAll(u.Keys))
	if len(updated) == 0 {
		return stmt + "DO NOTHING"
	}
	var sets []string
	for _, c := range updated {
		sets = append(sets, fmt.Sprintf("%s = EXCLUDED.%s", d.quote(c), d.quote(c)))
	}
	if u.Touch != "" {
		sets = append(sets, d.quote(u.Touch)+" = CURRENT_TIMESTAMP")
	}
	return stmt + "DO UPDATE SET " + strings.Join(sets, ", ")
}

func (d postgres) DeleteExcept(table string, keys []string, rows int) string {
	if rows == 0 {
		return "DELETE FROM " + d.quote(table)
	}
	if len(keys) == 1 {
		return fmt.Sprintf("DELETE FROM %s WHERE %s NOT IN (%s)", d.quote(table), d.quote(keys[0]), d.params(1, rows))
	}
	tuples := make([]string, rows)
	for i := range tuples {
		tuples[i] = "(" + d.params(1+i*len(keys), len(keys)) + ")"
	}
	return fmt.Sprintf("DELETE FROM %s WHERE (%s) NOT IN (%s)", d.quote(table), d.quoteAll(keys), strings.Join(tuples, ", "))
}
