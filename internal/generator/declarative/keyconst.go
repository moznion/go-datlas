package declarative

import (
	"bytes"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"strconv"
	"strings"
	"unicode"

	"github.com/moznion/go-datlas/internal/generator/loader"
	"github.com/moznion/go-datlas/internal/generator/naming"
	"github.com/moznion/go-datlas/internal/generator/output"
)

// keyConst is a constant generated for the logical key of one record of master data.
type keyConst struct {
	Name string
	Lit  string
}

// keyConsts decides the constants of the logical keys of ds, e.g. RoleCodeAdmin for the role
// whose Code is "admin". Constants are generated for master data with a single-field key of a
// string or integer type whose values are plain literals; enum keys already have constants.
func keyConsts(ds *Dataset, nm *namer, enums *loader.Enums) ([]keyConst, error) {
	if ds.Kind != MasterData || ds.NoConsts || len(ds.Schema.Keys) != 1 || len(ds.Records) == 0 {
		return nil, nil
	}
	key := ds.Schema.Keys[0]
	b, ok := key.Type().Underlying().(*types.Basic)
	if !ok || b.Info()&(types.IsString|types.IsInteger) == 0 {
		return nil, nil
	}
	if _, isEnum, err := enums.Lookup(key.Type()); err != nil || isEnum {
		return nil, err
	}
	singular := ds.Singular
	if singular == "" {
		singular = naming.Singular(naming.Camel(ds.Name))
	}
	prefix := singular + key.Name()

	owner := fmt.Sprintf("dataset %q", ds.Name)
	var (
		consts []keyConst
		errs   []error
	)
	for _, r := range ds.Records {
		suffix, ok := constSuffix(r.Keys[0])
		if !ok {
			return nil, nil // e.g. a value decoded with encoding.TextUnmarshaler
		}
		name := prefix + suffix
		if err := nm.claim(name, fmt.Sprintf("%s: %s = %s", r.Pos, key.Name(), r.Keys[0])); err != nil {
			errs = append(errs, err)
			continue
		}
		consts = append(consts, keyConst{Name: name, Lit: r.Keys[0]})
	}
	if err := errors.Join(errs...); err != nil {
		return nil, fmt.Errorf("%w\n%s: the constants of the logical keys can be disabled with //datlas:master %s consts=false", err, owner, ds.Name)
	}
	return consts, nil
}

// constSuffix derives the identifier suffix of a constant from a key literal:
// "admin" -> "Admin", "read-only" -> "ReadOnly", 42 -> "42", -1 -> "Minus1".
// It reports false when lit is not a plain string or integer literal.
func constSuffix(lit string) (string, bool) {
	e, err := parser.ParseExpr(lit)
	if err != nil {
		return "", false
	}
	switch e := e.(type) {
	case *ast.BasicLit:
		switch e.Kind {
		case token.INT:
			return e.Value, true
		case token.STRING:
			s, err := strconv.Unquote(e.Value)
			if err != nil {
				return "", false
			}
			return words(s), true
		}
	case *ast.UnaryExpr:
		if bl, ok := e.X.(*ast.BasicLit); ok && e.Op == token.SUB && bl.Kind == token.INT {
			return "Minus" + bl.Value, true
		}
	}
	return "", false
}

// words joins the runs of letters and digits of s, upper-casing the first letter of each run.
// Characters that cannot appear in identifiers separate the runs and are dropped.
func words(s string) string {
	var b strings.Builder
	for _, part := range strings.FieldsFunc(s, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		rs := []rune(part)
		rs[0] = unicode.ToUpper(rs[0])
		b.WriteString(string(rs))
	}
	if b.Len() == 0 {
		return "_" // e.g. a key consisting of punctuation only; collisions are reported
	}
	return b.String()
}

func renderKeyConsts(b *bytes.Buffer, im *output.Imports, ds *Dataset, consts []keyConst) {
	if len(consts) == 0 {
		return
	}
	key := ds.Schema.Keys[0]
	typ := im.TypeString(key.Type())
	fmt.Fprintf(b, "\n// Logical keys of %s (%s).\nconst (\n", ds.FuncName, key.Name())
	for _, c := range consts {
		fmt.Fprintf(b, "%s %s = %s\n", c.Name, typ, c.Lit)
	}
	b.WriteString(")\n")
}
