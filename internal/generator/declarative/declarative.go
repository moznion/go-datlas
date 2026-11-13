// Package declarative compiles declarative YAML data (Master Data and Seed Data)
// into typed Go values, validating it against Go struct schemas at generation time.
package declarative

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"

	"github.com/moznion/go-datlas/internal/generator/loader"
	"github.com/moznion/go-datlas/internal/generator/naming"
	"github.com/moznion/go-datlas/internal/generator/output"
)

// Kind distinguishes Master Data from Seed Data.
// Both are compiled the same way but remain independent concepts.
type Kind int

const (
	// MasterData is environment independent data that is part of the application's specification.
	MasterData Kind = iota
	// SeedData is environment dependent initialization data.
	SeedData
)

func (k Kind) String() string {
	if k == MasterData {
		return "master data"
	}
	return "seed data"
}

// directive returns the directive kind that declares datasets of k.
func (k Kind) directive() string {
	if k == MasterData {
		return loader.KindMaster
	}
	return loader.KindSeed
}

// tagKey returns the struct tag key of logical keys and references, e.g. `master:"key"`.
func (k Kind) tagKey() string {
	return k.directive()
}

// FileName returns the name of the file generated for k.
func (k Kind) FileName() string {
	if k == MasterData {
		return "zz_masterdata_gen.go"
	}
	return "zz_seeddata_gen.go"
}

func (k Kind) other() Kind {
	if k == MasterData {
		return SeedData
	}
	return MasterData
}

// prefix is the prefix of the unexported identifiers generated for k.
func (k Kind) prefix() string {
	if k == MasterData {
		return "datlasMaster"
	}
	return "datlasSeed"
}

// Schema is the Go type the records of a dataset are decoded into.
type Schema struct {
	Type   *types.TypeName
	Struct *types.Struct
	// Keys are the fields forming the logical key; more than one field makes a composite key.
	// It is empty when the schema defines no key.
	Keys []*types.Var
	// Refs are the fields referring to the logical key of another dataset.
	Refs []Ref
}

// Ref is a field whose values must be logical keys of another dataset.
type Ref struct {
	Field *types.Var
	// Package is the import path of the master data package holding the target dataset,
	// or empty for a dataset of the same kind in the same package (and, for seed data, the same environment).
	Package string
	Dataset string
}

func (r Ref) String() string {
	if r.Package == "" {
		return r.Dataset
	}
	return r.Package + "." + r.Dataset
}

// Dataset is the intermediate representation of one compiled YAML file.
type Dataset struct {
	Kind Kind
	// Name is the name given in the directive, e.g. "roles".
	Name string
	// Env is the environment of Seed Data, e.g. "local". It is empty for Master Data.
	Env    string
	Schema Schema
	// Singular overrides the singular form of the dataset name used in lookup function names.
	Singular string
	// NoConsts disables the constants of the logical keys of Master Data (consts=false).
	NoConsts bool
	// Files are the YAML files the records were compiled from.
	// Seed Data environments without a file for the dataset have none and no records.
	Files   []string
	Records []Record

	// FuncName is the name of the generated accessor, e.g. "Roles" or "LocalUsers".
	FuncName string
	// LookupName is the name of the generated key lookup function, or empty.
	LookupName string
}

// Config configures declarative data generation.
type Config struct {
	// Dir is the directory Package is resolved against.
	Dir string
	// Package is the package holding the schemas; the generated file is written into it.
	Package string
	// DataDir is the directory holding the YAML files, relative to the package directory.
	DataDir string
}

// Generate compiles every dataset of kind declared in cfg.Package.
// When the package declares no dataset of kind any more, the result asks to remove
// the previously generated file; it is an error if there is none.
func Generate(kind Kind, cfg Config) (output.Result, error) {
	fail := func(err error) (output.Result, error) { return output.Result{}, err }
	pattern := cfg.Package
	if pattern == "" {
		pattern = "."
	}
	pkgs, err := loader.Load(cfg.Dir, pattern)
	if err != nil {
		return fail(err)
	}
	if len(pkgs) != 1 {
		return fail(fmt.Errorf("%s: expected exactly one package, matched %d", pattern, len(pkgs)))
	}
	pkg := pkgs[0]
	if len(pkg.GoFiles) == 0 {
		return fail(fmt.Errorf("%s: package has no Go files", pkg.PkgPath))
	}
	pkgDir := filepath.Dir(pkg.GoFiles[0])
	dataDir := cfg.DataDir
	if dataDir == "" {
		dataDir = "."
	}
	if !filepath.IsAbs(dataDir) {
		dataDir = filepath.Join(pkgDir, dataDir)
	}
	if st, err := os.Stat(dataDir); err != nil || !st.IsDir() {
		return fail(fmt.Errorf("data directory %s does not exist", displayPath(dataDir)))
	}

	all, err := loader.Directives(pkgs)
	if err != nil {
		return fail(err)
	}
	var directives []loader.Directive
	for _, d := range all {
		if d.Kind == kind.directive() {
			directives = append(directives, d)
		}
	}
	path := filepath.Join(pkgDir, kind.FileName())
	if len(directives) == 0 {
		// YAML files left behind without a schema are errors, as they are while schemas exist.
		if _, err := discover(kind, dataDir, nil); err != nil {
			return fail(err)
		}
		if _, err := os.Stat(path); err == nil {
			return output.Result{Path: path}, nil
		}
		return fail(fmt.Errorf("no //datlas:%s types found in %s", kind.directive(), pkg.PkgPath))
	}

	enums := loader.NewEnums(cfg.Dir, pkgs, all)
	schemas, err := analyze(kind, pkg, directives, enums)
	if err != nil {
		return fail(err)
	}
	datasets, err := discover(kind, dataDir, schemas)
	if err != nil {
		return fail(err)
	}
	nm, err := assignNames(kind, pkg, pkgDir, datasets)
	if err != nil {
		return fail(err)
	}
	src, texts, err := render(kind, pkg, datasets, enums, nm)
	if err != nil {
		return fail(err)
	}
	if err := checkRefs(kind, cfg.Dir, datasets); err != nil {
		return fail(err)
	}
	if err := runTextChecks(pkg, pkgDir, path, src, texts); err != nil {
		return fail(err)
	}
	return output.Result{Path: path, Src: src}, nil
}

type namedSchema struct {
	name      string
	singular  string
	noConsts  bool
	directive loader.Directive
	schema    Schema
}

func analyze(kind Kind, pkg *packages.Package, directives []loader.Directive, enums *loader.Enums) ([]namedSchema, error) {
	var (
		result []namedSchema
		errs   []error
		names  = map[string]loader.Directive{}
	)
	for _, d := range directives {
		if len(d.Args) == 0 || strings.Contains(d.Args[0], "=") {
			errs = append(errs, fmt.Errorf("%s: //datlas:%s requires a dataset name, e.g. //datlas:%s users", d.Pos, kind.directive(), kind.directive()))
			continue
		}
		name := d.Args[0]
		var (
			singular string
			noConsts bool
			seenOpt  = map[string]bool{}
		)
		for _, opt := range d.Args[1:] {
			k, v, _ := strings.Cut(opt, "=")
			switch {
			case seenOpt[k]:
			case k == "singular" && naming.IsIdentifier(v):
				singular = naming.Camel(v)
				seenOpt[k] = true
				continue
			case k == "consts" && v == "false" && kind == MasterData:
				noConsts = true
				seenOpt[k] = true
				continue
			}
			available := "singular=<Name>"
			if kind == MasterData {
				available += ", consts=false"
			}
			errs = append(errs, fmt.Errorf("%s: //datlas:%s: unknown or repeated option %q (available: %s)", d.Pos, kind.directive(), opt, available))
		}
		if !validDatasetName(name) {
			errs = append(errs, fmt.Errorf("%s: invalid dataset name %q: use letters, digits, '_' and '-', starting with a letter", d.Pos, name))
			continue
		}
		if prev, dup := names[name]; dup {
			errs = append(errs, fmt.Errorf("%s: dataset %q is already declared at %s", d.Pos, name, prev.Pos))
			continue
		}
		names[name] = d

		ctx := fmt.Sprintf("%s: %s", d.Pos, d.Type.Name())
		if err := loader.CheckType(pkg, d.Type.Type(), ctx); err != nil {
			errs = append(errs, err)
			continue
		}
		if err := checkSupported(d.Type.Type(), ctx, enums); err != nil {
			errs = append(errs, err)
			continue
		}
		schema, err := schemaOf(kind, d, enums)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		result = append(result, namedSchema{name: name, singular: singular, noConsts: noConsts, directive: d, schema: schema})
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return result, checkRefTargets(kind, result, enums)
}

func validDatasetName(name string) bool {
	for i, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
		case i > 0 && (r >= '0' && r <= '9' || r == '_' || r == '-'):
		default:
			return false
		}
	}
	return name != ""
}

// scalarKind returns the kind of values usable as keys and references:
// "string", "integer" or "bool", or "" for any other type.
func scalarKind(t types.Type, enums *loader.Enums) string {
	if isText(t, enums) {
		return ""
	}
	b, ok := t.Underlying().(*types.Basic)
	switch {
	case !ok:
		return ""
	case b.Info()&types.IsString != 0:
		return "string"
	case b.Info()&types.IsInteger != 0:
		return "integer"
	case b.Info()&types.IsBoolean != 0:
		return "bool"
	}
	return ""
}

// refElem returns the type whose values a reference field holds: T for T, *T and []T.
func refElem(t types.Type) types.Type {
	switch u := types.Unalias(t).(type) {
	case *types.Pointer:
		return u.Elem()
	}
	if s, ok := t.Underlying().(*types.Slice); ok && !isBytes(t) {
		return s.Elem()
	}
	return t
}

// schemaOf reads the logical key and reference fields, e.g. `master:"key"` and `seed:"ref=companies"`.
// Keys must be fields of the schema or of its inline structs; references may be at any depth.
func schemaOf(kind Kind, d loader.Directive, enums *loader.Enums) (Schema, error) {
	schema := Schema{Type: d.Type, Struct: d.Struct}
	var (
		errs      []error
		keyTagged bool
		seen      = map[*types.Struct]bool{}
	)
	var walk func(st *types.Struct, path string, keyLevel bool)
	walk = func(st *types.Struct, path string, keyLevel bool) {
		if seen[st] {
			return
		}
		seen[st] = true
		mapped := map[*types.Var]structField{}
		for _, f := range yamlFields(st) {
			mapped[f.v] = f
		}
		for i := range st.NumFields() {
			v := st.Field(i)
			ctx := fmt.Sprintf("%s: %s.%s", d.Pos, path, v.Name())
			f, isMapped := mapped[v]
			kt, tagged, err := parseKindTag(kind, st.Tag(i))
			switch {
			case err != nil:
				errs = append(errs, fmt.Errorf("%s: %w", ctx, err))
			case !tagged:
				if _, other, _ := parseKindTag(kind.other(), st.Tag(i)); other {
					errs = append(errs, fmt.Errorf("%s: %s tags have no effect on %s; use %s", ctx, kind.other().tagKey(), kind, kind.tagKey()))
				}
			case !isMapped || f.tag.inline:
				errs = append(errs, fmt.Errorf("%s: keys and references must be exported fields mapped from YAML", ctx))
			default:
				if kt.key {
					keyTagged = true
					switch {
					case !keyLevel:
						errs = append(errs, fmt.Errorf("%s: a logical key must be a field of %s or of a struct inlined into it", ctx, d.Type.Name()))
					case !f.required():
						errs = append(errs, fmt.Errorf("%s: a logical key field must be required (not a pointer, not omitempty)", ctx))
					case scalarKind(v.Type(), enums) == "":
						errs = append(errs, fmt.Errorf("%s: a logical key must be a string, integer or bool type, not %s", ctx, v.Type()))
					default:
						schema.Keys = append(schema.Keys, v)
					}
				}
				if kt.ref != "" {
					if ref, err := parseRef(kind, d, v, kt.ref, enums); err != nil {
						errs = append(errs, fmt.Errorf("%s: %w", ctx, err))
					} else {
						schema.Refs = append(schema.Refs, ref)
					}
				}
			}
			if !isMapped {
				continue
			}
			if f.tag.inline {
				walk(types.Unalias(v.Type()).Underlying().(*types.Struct), path, keyLevel)
				continue
			}
			for _, inner := range structsIn(v.Type(), enums) {
				walk(inner, path+"."+v.Name(), false)
			}
		}
	}
	walk(d.Struct, d.Type.Name(), true)
	if !keyTagged && kind == MasterData {
		errs = append(errs, fmt.Errorf("%s: master data %s must declare a logical key with a `master:\"key\"` tag", d.Pos, d.Type.Name()))
	}
	return schema, errors.Join(errs...)
}

// structsIn returns the struct types values of t are built from, looking through
// pointers, slices, arrays and maps.
func structsIn(t types.Type, enums *loader.Enums) []*types.Struct {
	t = types.Unalias(t)
	if isTime(t) || isDuration(t) || isText(t, enums) {
		return nil
	}
	switch u := t.Underlying().(type) {
	case *types.Struct:
		return []*types.Struct{u}
	case *types.Pointer:
		return structsIn(u.Elem(), enums)
	case *types.Slice:
		return structsIn(u.Elem(), enums)
	case *types.Array:
		return structsIn(u.Elem(), enums)
	case *types.Map:
		return structsIn(u.Elem(), enums)
	}
	return nil
}

func parseRef(kind Kind, d loader.Directive, v *types.Var, target string, enums *loader.Enums) (Ref, error) {
	if scalarKind(refElem(v.Type()), enums) == "" {
		return Ref{}, fmt.Errorf("a reference must be a string, integer or bool type (or a pointer or slice of one), not %s", v.Type())
	}
	ref := Ref{Field: v, Dataset: target}
	if i := strings.LastIndex(target, "."); i >= 0 {
		ref.Package, ref.Dataset = target[:i], target[i+1:]
	}
	if !validDatasetName(ref.Dataset) {
		return Ref{}, fmt.Errorf("invalid reference %q: expected <dataset> or <import path>.<dataset>", target)
	}
	if ref.Package == d.Type.Pkg().Path() && kind == MasterData {
		ref.Package = ""
	}
	return ref, nil
}

// checkRefTargets verifies that references within the package point at datasets
// with a single-field key of a compatible type.
func checkRefTargets(kind Kind, schemas []namedSchema, enums *loader.Enums) error {
	byName := map[string]namedSchema{}
	for _, s := range schemas {
		byName[s.name] = s
	}
	var errs []error
	for _, s := range schemas {
		for _, ref := range s.schema.Refs {
			if ref.Package != "" {
				continue // checked against the generated master data of that package by checkRefs
			}
			ctx := fmt.Sprintf("%s: %s", s.directive.Pos, ref.Field.Name())
			target, ok := byName[ref.Dataset]
			if !ok {
				errs = append(errs, fmt.Errorf("%s: reference to unknown %s dataset %q", ctx, kind, ref.Dataset))
				continue
			}
			if err := compatibleKey(ref, target.schema.Keys, enums); err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", ctx, err))
			}
		}
	}
	return errors.Join(errs...)
}

func compatibleKey(ref Ref, keys []*types.Var, enums *loader.Enums) error {
	switch {
	case len(keys) == 0:
		return fmt.Errorf("dataset %q declares no logical key to refer to", ref.Dataset)
	case len(keys) > 1:
		return fmt.Errorf("dataset %q has a composite key, which cannot be referred to by a single field", ref.Dataset)
	}
	if got, want := scalarKind(refElem(ref.Field.Type()), enums), scalarKind(keys[0].Type(), enums); got != want {
		return fmt.Errorf("reference of %s type cannot refer to the %s key of dataset %q", got, want, ref.Dataset)
	}
	return nil
}

// discover locates the YAML files of every dataset.
//
// Master Data lives in <dataDir>/<name>.yaml.
// Seed Data lives in <dataDir>/<env>/<name>.yaml, one directory per environment;
// every dataset gets a (possibly empty) Dataset in every environment.
// YAML files that do not belong to any declared dataset are reported as errors.
// Files and directories whose name starts with "." or "_" are ignored.
func discover(kind Kind, dataDir string, schemas []namedSchema) ([]*Dataset, error) {
	byName := map[string]namedSchema{}
	datasetNames := map[string]bool{}
	for _, s := range schemas {
		byName[s.name] = s
		datasetNames[s.name] = true
	}

	type location struct{ env, dir string }
	var (
		locations []location
		errs      []error
	)
	if kind == MasterData {
		locations = []location{{dir: dataDir}}
	} else {
		entries, err := os.ReadDir(dataDir)
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if ignored(e.Name()) {
				continue
			}
			if !e.IsDir() {
				if isYAML(e.Name()) {
					errs = append(errs, fmt.Errorf("%s: seed data must be placed in an environment directory such as %s",
						displayPath(filepath.Join(dataDir, e.Name())), displayPath(filepath.Join(dataDir, "local", e.Name()))))
				}
				continue
			}
			dir := filepath.Join(dataDir, e.Name())
			if e.Name() == "testdata" || hasGoFiles(dir) {
				continue
			}
			files, err := yamlFiles(dir)
			if err != nil {
				return nil, err
			}
			if len(files) == 0 {
				nested, err := nestedYAML(dir, datasetNames, nestedDepth)
				if err != nil {
					return nil, err
				}
				for _, n := range nested {
					errs = append(errs, fmt.Errorf("%s: seed data must be placed directly in an environment directory such as %s%s",
						displayPath(n), dirLabel(filepath.Join(dataDir, "local")), misplacedHint(n)))
				}
				continue
			}
			if !validDatasetName(e.Name()) {
				errs = append(errs, fmt.Errorf("%s: invalid environment name %q", displayPath(dir), e.Name()))
				continue
			}
			locations = append(locations, location{env: e.Name(), dir: dir})
		}
	}

	var (
		datasets []*Dataset
		found    = map[string]bool{}
	)
	for _, loc := range locations {
		files, err := yamlFiles(loc.dir)
		if err != nil {
			return nil, err
		}
		nested, err := nestedYAML(loc.dir, datasetNames, nestedDepth)
		if err != nil {
			return nil, err
		}
		for _, n := range nested {
			errs = append(errs, fmt.Errorf("%s: dataset files in subdirectories are not loaded; move it to %s%s",
				displayPath(n), dirLabel(loc.dir), misplacedHint(n)))
		}
		byDataset := map[string]*Dataset{}
		for _, s := range schemas {
			ds := &Dataset{Kind: kind, Name: s.name, Env: loc.env, Schema: s.schema, Singular: s.singular, NoConsts: s.noConsts}
			byDataset[s.name] = ds
			datasets = append(datasets, ds)
		}
		for _, file := range files {
			base := strings.TrimSuffix(file, filepath.Ext(file))
			path := filepath.Join(loc.dir, file)
			ds, ok := byDataset[base]
			if !ok {
				errs = append(errs, fmt.Errorf("%s: no //datlas:%s type declares dataset %q", displayPath(path), kind.directive(), base))
				continue
			}
			if len(ds.Files) > 0 {
				errs = append(errs, fmt.Errorf("%s: dataset %q is also defined by %s", displayPath(path), base, displayPath(ds.Files[0])))
				continue
			}
			ds.Files = append(ds.Files, path)
			found[base] = true
		}
	}
	for _, s := range schemas {
		if !found[s.name] {
			where := filepath.Join(dataDir, s.name+".yaml")
			if kind == SeedData {
				where = filepath.Join(dataDir, "<env>", s.name+".yaml")
			}
			errs = append(errs, fmt.Errorf("%s: no YAML file found for dataset %q (expected %s)", s.directive.Pos, s.name, displayPath(where)))
		}
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}

	// Order datasets by declaration, then by environment, for stable output.
	order := map[string]int{}
	for i, s := range schemas {
		order[s.name] = i
	}
	sort.SliceStable(datasets, func(i, j int) bool {
		if order[datasets[i].Name] != order[datasets[j].Name] {
			return order[datasets[i].Name] < order[datasets[j].Name]
		}
		return datasets[i].Env < datasets[j].Env
	})
	return datasets, nil
}

// nestedDepth is how many directory levels below a data directory are searched for misplaced files.
const nestedDepth = 2

// nestedYAML finds files named after a declared dataset (<dataset>.yaml) in the subdirectories
// of dir, up to depth levels down. datlas does not load them, so they are most likely misplaced.
// Other YAML files (deployment manifests, API specs, ...) are left alone. Ignored directories,
// testdata and directories holding Go files (other packages) are skipped.
func nestedYAML(dir string, datasets map[string]bool, depth int) ([]string, error) {
	if depth == 0 || len(datasets) == 0 {
		return nil, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var found []string
	for _, e := range entries {
		if !e.IsDir() || ignored(e.Name()) || e.Name() == "testdata" {
			continue
		}
		sub := filepath.Join(dir, e.Name())
		if hasGoFiles(sub) {
			continue
		}
		files, err := yamlFiles(sub)
		if err != nil {
			return nil, err
		}
		for _, f := range files {
			if datasets[strings.TrimSuffix(f, filepath.Ext(f))] {
				found = append(found, filepath.Join(sub, f))
			}
		}
		deeper, err := nestedYAML(sub, datasets, depth-1)
		if err != nil {
			return nil, err
		}
		found = append(found, deeper...)
	}
	return found, nil
}

// misplacedHint explains why a nested file was flagged and how to exclude unrelated ones.
func misplacedHint(path string) string {
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	return fmt.Sprintf(" (flagged because it is named after dataset %q; if it is unrelated, prefix its directory with \".\" or \"_\" to ignore it)", name)
}

// dirLabel names a directory in messages, avoiding the unhelpful ".".
func dirLabel(dir string) string {
	if p := displayPath(dir); p != "." {
		return p
	}
	return "the package directory"
}

// hasGoFiles reports whether dir holds Go files, i.e. is another package.
func hasGoFiles(dir string) bool {
	matches, _ := filepath.Glob(filepath.Join(dir, "*.go"))
	return len(matches) > 0
}

func ignored(name string) bool {
	return strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")
}

func isYAML(name string) bool {
	ext := filepath.Ext(name)
	return ext == ".yaml" || ext == ".yml"
}

func yamlFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() && isYAML(e.Name()) && !ignored(e.Name()) {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)
	return files, nil
}

// Identifiers generated once per package.
const (
	masterDatasetsName   = "MasterDatasets"
	seedDatasetsName     = "SeedDatasets"
	seedEnvironmentsName = "SeedEnvironments"
	seedSetName          = "SeedSet"
	seedForName          = "SeedFor"
)

// namer records the generated identifiers and detects collisions between them, with
// identifiers declared in the package, and with identifiers generated for the other kind.
type namer struct {
	kind       Kind
	pkg        *packages.Package
	otherPath  string
	otherNames map[string]bool
	used       map[string]string
}

func (n *namer) claim(name, owner string) error {
	if prev, ok := n.used[name]; ok {
		return fmt.Errorf("%s: generated identifier %s conflicts with %s", owner, name, prev)
	}
	if n.otherNames[name] {
		return fmt.Errorf("%s: generated identifier %s conflicts with the %s generated into %s",
			owner, name, n.kind.other(), displayPath(n.otherPath))
	}
	if obj := n.pkg.Types.Scope().Lookup(name); obj != nil {
		return fmt.Errorf("%s: generated identifier %s conflicts with the declaration at %s",
			owner, name, n.pkg.Fset.Position(obj.Pos()))
	}
	n.used[name] = owner
	return nil
}

// assignNames decides the generated identifiers and checks they do not collide with each other,
// with identifiers declared in the package, or with identifiers generated for the other kind.
func assignNames(kind Kind, pkg *packages.Package, pkgDir string, datasets []*Dataset) (*namer, error) {
	otherPath := filepath.Join(pkgDir, kind.other().FileName())
	otherNames, err := generatedNames(otherPath)
	if err != nil {
		return nil, err
	}

	var errs []error
	nm := &namer{kind: kind, pkg: pkg, otherPath: otherPath, otherNames: otherNames, used: map[string]string{}}
	claim := func(name, owner string) {
		if err := nm.claim(name, owner); err != nil {
			errs = append(errs, err)
		}
	}
	if kind == SeedData {
		for _, n := range []string{seedDatasetsName, seedEnvironmentsName, seedSetName, seedForName} {
			claim(n, "seed data")
		}
	} else {
		claim(masterDatasetsName, "master data")
	}
	for _, ds := range datasets {
		ds.FuncName = naming.Camel(ds.Env) + naming.Camel(ds.Name)
		owner := fmt.Sprintf("dataset %q", ds.Name)
		if ds.Env != "" {
			owner += fmt.Sprintf(" (environment %q)", ds.Env)
		}
		claim(ds.FuncName, owner)
		if keys := ds.Schema.Keys; len(keys) > 0 {
			var fields []string
			for _, k := range keys {
				fields = append(fields, k.Name())
			}
			singular := ds.Singular
			if singular == "" {
				singular = naming.Singular(naming.Camel(ds.Name))
			}
			ds.LookupName = naming.Camel(ds.Env) + singular + "By" + strings.Join(fields, "And")
			if ds.LookupName == ds.FuncName {
				ds.LookupName = ds.FuncName + "Lookup"
			}
			claim(ds.LookupName, owner)
		}
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return nm, nil
}

// generatedNames returns the top-level identifiers declared in the datlas-generated file at path.
func generatedNames(path string) (map[string]bool, error) {
	src, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !output.IsGenerated(src) {
		return nil, nil
	}
	f, err := parser.ParseFile(token.NewFileSet(), path, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, nil // a broken generated file is regenerated separately
	}
	names := map[string]bool{}
	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Recv == nil {
				names[d.Name.Name] = true
			}
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					names[s.Name.Name] = true
				case *ast.ValueSpec:
					for _, n := range s.Names {
						names[n.Name] = true
					}
				}
			}
		}
	}
	return names, nil
}

// displayPath shortens path relative to the working directory when possible.
func displayPath(path string) string {
	wd, err := os.Getwd()
	if err != nil {
		return path
	}
	if rel, err := filepath.Rel(wd, path); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return path
}
