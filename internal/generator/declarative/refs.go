package declarative

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/tools/go/packages"

	"github.com/moznion/go-datlas/internal/generator/output"
)

// keySet is the set of logical keys of a dataset, as Go literals.
type keySet map[string]bool

// checkRefs verifies that every reference value is a logical key of the referenced dataset.
//
// References within the package are checked against the datasets being generated
// (for seed data, those of the same environment). References to another package are
// checked against the master data generated into that package, which therefore must be
// generated first.
func checkRefs(kind Kind, dir string, datasets []*Dataset) error {
	local := map[string]keySet{} // env + "\x00" + dataset -> keys
	for _, ds := range datasets {
		if len(ds.Schema.Keys) != 1 {
			continue
		}
		keys := keySet{}
		for _, r := range ds.Records {
			keys[r.Keys[0]] = true
		}
		local[ds.Env+"\x00"+ds.Name] = keys
	}

	external := map[string]keySet{}
	var errs []error
	for _, ds := range datasets {
		for _, r := range ds.Records {
			for _, rv := range r.Refs {
				var keys keySet
				if rv.Ref.Package == "" {
					keys = local[ds.Env+"\x00"+rv.Ref.Dataset]
				} else {
					id := rv.Ref.String()
					var ok bool
					if keys, ok = external[id]; !ok {
						var err error
						if keys, err = externalKeys(dir, rv.Ref.Package, rv.Ref.Dataset); err != nil {
							return fmt.Errorf("%s: reference to %s: %w", rv.Pos, id, err)
						}
						external[id] = keys
					}
				}
				if !keys[rv.Lit] {
					where := fmt.Sprintf("dataset %q", rv.Ref.String())
					if rv.Ref.Package == "" && ds.Env != "" {
						where += fmt.Sprintf(" of the %q environment", ds.Env)
					}
					errs = append(errs, fmt.Errorf("%s: %s refers to %s, which does not exist in %s",
						rv.Pos, rv.Ref.Field.Name(), rv.Lit, where))
				}
			}
		}
	}
	return errors.Join(errs...)
}

// externalKeys reads the logical keys of the master data dataset generated into the package at pkgPath.
func externalKeys(dir, pkgPath, dataset string) (keySet, error) {
	pkgs, err := packages.Load(&packages.Config{Mode: packages.NeedName | packages.NeedFiles, Dir: dir}, pkgPath)
	if err != nil {
		return nil, err
	}
	if len(pkgs) != 1 || len(pkgs[0].GoFiles) == 0 {
		return nil, fmt.Errorf("package %s not found", pkgPath)
	}
	for _, e := range pkgs[0].Errors {
		return nil, e
	}
	path := filepath.Join(filepath.Dir(pkgs[0].GoFiles[0]), MasterData.FileName())
	src, err := os.ReadFile(path)
	if os.IsNotExist(err) || (err == nil && !output.IsGenerated(src)) {
		return nil, fmt.Errorf("%s has no generated master data; run `datlas master` for it first", pkgPath)
	}
	if err != nil {
		return nil, err
	}
	f, err := parser.ParseFile(token.NewFileSet(), path, src, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Doc == nil || !hasKeysMarker(fn.Doc, dataset) {
			continue
		}
		keys := keySet{}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			if cc, ok := n.(*ast.CaseClause); ok {
				for _, e := range cc.List {
					switch lit := e.(type) {
					case *ast.BasicLit:
						keys[lit.Value] = true
					case *ast.Ident:
						keys[lit.Name] = true
					case *ast.UnaryExpr: // negative integers
						if bl, ok := lit.X.(*ast.BasicLit); ok {
							keys[lit.Op.String()+bl.Value] = true
						}
					}
				}
			}
			return true
		})
		return keys, nil
	}
	return nil, fmt.Errorf("%s declares no master data %q with a single-field logical key (regenerate it if it is out of date)", pkgPath, dataset)
}

func hasKeysMarker(doc *ast.CommentGroup, dataset string) bool {
	for _, c := range doc.List {
		if strings.TrimSpace(strings.TrimPrefix(c.Text, keysMarker)) == dataset && strings.HasPrefix(c.Text, keysMarker) {
			return true
		}
	}
	return false
}
