package loader

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/parser"
	"go/token"
	"go/types"
	"strconv"
	"strings"

	"golang.org/x/tools/go/packages"
)

// Enums tells which named types are declared with //datlas:enum and which values they allow.
// Enum types may live in any package; packages other than the loaded ones are parsed on demand.
type Enums struct {
	dir    string
	byPkg  map[string]map[string]bool // package path -> names of enum types
	values map[*types.TypeName]Enum
}

// Enum is the set of values an enum type allows.
type Enum struct {
	// Literals are the Go literals of the allowed values, as rendered by generators
	// (strconv.Quote for strings, decimal for integers).
	Literals map[string]bool
	// Names lists the constants in alphabetical order.
	Names []string
	// Values lists the distinct literals of the constants, in the order of Names.
	Values []string
	// Pairs lists the constants with their values ("TierPro=2"), for error messages.
	Pairs []string
}

// NewEnums returns an enum resolver. dir is used to locate packages;
// directives are the directives of the already loaded packages.
func NewEnums(dir string, pkgs []*packages.Package, directives []Directive) *Enums {
	e := &Enums{dir: dir, byPkg: map[string]map[string]bool{}, values: map[*types.TypeName]Enum{}}
	for _, p := range pkgs {
		e.byPkg[p.PkgPath] = map[string]bool{}
	}
	for _, d := range directives {
		if d.Kind == KindEnum {
			e.byPkg[d.Type.Pkg().Path()][d.Type.Name()] = true
		}
	}
	return e
}

// Lookup returns the enum declared by t, if t is an enum type.
// A nil *Enums knows no enums.
func (e *Enums) Lookup(t types.Type) (Enum, bool, error) {
	if e == nil {
		return Enum{}, false, nil
	}
	named, ok := types.Unalias(t).(*types.Named)
	if !ok || named.Obj().Pkg() == nil {
		return Enum{}, false, nil
	}
	obj := named.Obj()
	if v, ok := e.values[obj]; ok {
		return v, true, nil
	}
	enums, err := e.enumsOf(obj.Pkg().Path())
	if err != nil {
		return Enum{}, false, err
	}
	if !enums[obj.Name()] {
		return Enum{}, false, nil
	}

	v := Enum{Literals: map[string]bool{}}
	scope := obj.Pkg().Scope()
	// Scope.Names is sorted, so Names and Values are too.
	for _, name := range scope.Names() {
		c, ok := scope.Lookup(name).(*types.Const)
		if !ok || !types.Identical(c.Type(), named) {
			continue
		}
		var lit string
		switch c.Val().Kind() {
		case constant.String:
			lit = strconv.Quote(constant.StringVal(c.Val()))
		case constant.Int:
			lit = c.Val().ExactString()
		default:
			continue
		}
		v.Names = append(v.Names, name)
		v.Pairs = append(v.Pairs, name+"="+lit)
		// Constants may alias one another; each value is listed once.
		if !v.Literals[lit] {
			v.Values = append(v.Values, lit)
		}
		v.Literals[lit] = true
	}
	if len(v.Names) == 0 {
		return Enum{}, false, fmt.Errorf("enum %s declares no constants", obj.Name())
	}
	e.values[obj] = v
	return v, true, nil
}

// enumsOf returns the names of the enum types declared in the package at path.
func (e *Enums) enumsOf(path string) (map[string]bool, error) {
	if m, ok := e.byPkg[path]; ok {
		return m, nil
	}
	m := map[string]bool{}
	e.byPkg[path] = m
	// Packages without Go files of their own (such as the standard library in
	// unusual setups) simply have no enums.
	pkgs, err := packages.Load(&packages.Config{Mode: packages.NeedName | packages.NeedFiles, Dir: e.dir}, path)
	if err != nil {
		return nil, err
	}
	fset := token.NewFileSet()
	for _, p := range pkgs {
		for _, f := range p.GoFiles {
			file, err := parser.ParseFile(fset, f, nil, parser.ParseComments|parser.SkipObjectResolution)
			if err != nil {
				return nil, err
			}
			for _, decl := range file.Decls {
				gd, ok := decl.(*ast.GenDecl)
				if !ok || gd.Tok != token.TYPE {
					continue
				}
				for _, spec := range gd.Specs {
					ts := spec.(*ast.TypeSpec)
					docs := []*ast.CommentGroup{ts.Doc}
					if len(gd.Specs) == 1 {
						docs = append(docs, gd.Doc)
					}
					for _, doc := range docs {
						for _, c := range directiveComments(doc) {
							if strings.TrimSpace(strings.TrimPrefix(c.Text, DirectivePrefix)) == KindEnum {
								m[ts.Name.Name] = true
							}
						}
					}
				}
			}
		}
	}
	return m, nil
}
