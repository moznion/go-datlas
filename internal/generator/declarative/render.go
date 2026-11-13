package declarative

import (
	"bytes"
	"errors"
	"fmt"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/tools/go/packages"

	"github.com/moznion/go-datlas/internal/generator/loader"
	"github.com/moznion/go-datlas/internal/generator/naming"
	"github.com/moznion/go-datlas/internal/generator/output"
)

// keysMarker precedes the dataset name in the doc comment of the key lookup function of
// master data with a single-field key. Seed data referring to that master data reads the
// keys back from the generated file (see externalKeys).
// It has the form of a directive so that it does not show up in documentation.
const keysMarker = "//datlas:keys "

// render compiles the YAML of every dataset and renders the generated file.
// Records are compiled here because literals depend on the imports of the file.
func render(kind Kind, pkg *packages.Package, datasets []*Dataset, enums *loader.Enums, nm *namer) ([]byte, []textCheck, error) {
	im := output.NewImports(pkg.PkgPath, append(pkg.Types.Scope().Names(), "key", "env")...)

	var (
		b        bytes.Buffer
		errs     []error
		texts    []textCheck
		usesPtr  bool
		usesText bool
	)
	for _, ds := range datasets {
		for _, file := range ds.Files {
			src, err := os.ReadFile(file)
			if err != nil {
				return nil, nil, err
			}
			c := &compiler{
				path:     displayPath(file),
				im:       im,
				enums:    enums,
				ptrFunc:  kind.prefix() + "Ptr",
				textFunc: kind.prefix() + "Text",
			}
			ds.Records = append(ds.Records, c.compileFile(src, ds.Schema)...)
			usesPtr = usesPtr || c.usesPtr
			usesText = usesText || c.usesText
			texts = append(texts, c.texts...)
			if err := c.err(); err != nil {
				errs = append(errs, err)
			}
		}
		if err := checkDuplicateKeys(ds); err != nil {
			errs = append(errs, err)
		}
	}
	if err := errors.Join(errs...); err != nil {
		return nil, nil, err
	}
	consts := make([][]keyConst, len(datasets))
	for i, ds := range datasets {
		var err error
		if consts[i], err = keyConsts(ds, nm, enums); err != nil {
			errs = append(errs, err)
		}
	}
	if err := errors.Join(errs...); err != nil {
		return nil, nil, err
	}

	order := datasetOrder(datasets)
	if kind == MasterData {
		renderDatasetOrder(&b, masterDatasetsName, "master data", order)
	}
	for i, ds := range datasets {
		renderKeyConsts(&b, im, ds, consts[i])
		renderDataset(&b, im, ds, pkg.Types.Scope())
	}
	if kind == SeedData {
		renderDatasetOrder(&b, seedDatasetsName, "seed data", order)
		renderSeedSet(&b, im, datasets, order)
	}
	if usesPtr {
		fmt.Fprintf(&b, "\nfunc %sPtr[T any](v T) *T { return &v }\n", kind.prefix())
	}
	if usesText {
		fmt.Fprintf(&b, `
// %[1]sText decodes s with encoding.TextUnmarshaler.
// The values were validated when the code was generated, so decoding cannot fail.
func %[1]sText[T any, PT interface {
	*T
	UnmarshalText([]byte) error
}](s string) T {
	var v T
	if err := PT(&v).UnmarshalText([]byte(s)); err != nil {
		panic("datlas: " + err.Error())
	}
	return v
}
`, kind.prefix())
	}
	return output.File(pkg.Name, im, b.Bytes()), texts, nil
}

// checkDuplicateKeys fails when two records share a logical key.
func checkDuplicateKeys(ds *Dataset) error {
	if len(ds.Schema.Keys) == 0 {
		return nil
	}
	var names []string
	for _, k := range ds.Schema.Keys {
		names = append(names, k.Name())
	}
	var errs []error
	seen := map[string]string{}
	for _, r := range ds.Records {
		key := strings.Join(r.Keys, "\x00")
		if prev, dup := seen[key]; dup {
			errs = append(errs, fmt.Errorf("%s: duplicate logical key %s = %s (first defined at %s)",
				r.Pos, strings.Join(names, ", "), strings.Join(r.Keys, ", "), prev))
			continue
		}
		seen[key] = r.Pos
	}
	return errors.Join(errs...)
}

func (ds *Dataset) source() string {
	if len(ds.Files) == 0 {
		return fmt.Sprintf("no file (the %q environment has no %s.yaml)", ds.Env, ds.Name)
	}
	file := filepath.Base(ds.Files[0])
	if ds.Env != "" {
		file = ds.Env + "/" + file
	}
	return file
}

func (ds *Dataset) constructor(i int) string {
	return fmt.Sprintf("%s%s%d", ds.Kind.prefix(), ds.FuncName, i)
}

func renderDataset(b *bytes.Buffer, im *output.Imports, ds *Dataset, scope *types.Scope) {
	typ := im.TypeString(ds.Schema.Type.Type())

	fmt.Fprintf(b, "\n// %s returns the %s %q", ds.FuncName, ds.Kind, ds.Name)
	if ds.Env != "" {
		fmt.Fprintf(b, " of the %q environment", ds.Env)
	}
	fmt.Fprintf(b, ", compiled from %s.\n", ds.source())
	b.WriteString("// Each call returns a new copy that the caller may modify freely.\n")
	fmt.Fprintf(b, "func %s() []%s {\nreturn []%s{", ds.FuncName, typ, typ)
	if len(ds.Records) > 0 {
		b.WriteString("\n")
	}
	for i := range ds.Records {
		fmt.Fprintf(b, "%s(),\n", ds.constructor(i))
	}
	b.WriteString("}\n}\n")

	if ds.LookupName != "" {
		renderLookup(b, im, ds, typ, scope)
	}
	for i, r := range ds.Records {
		fmt.Fprintf(b, "\nfunc %s() %s {\nreturn %s%s\n}\n", ds.constructor(i), typ, typ, r.Expr)
	}
}

func renderLookup(b *bytes.Buffer, im *output.Imports, ds *Dataset, typ string, scope *types.Scope) {
	keys := ds.Schema.Keys
	var names []string
	for _, k := range keys {
		names = append(names, k.Name())
	}
	verb := "equals the given value"
	if len(keys) > 1 {
		verb = "equal the given values"
	}
	fmt.Fprintf(b, "\n// %s returns the record of %s whose %s %s.\n// Each call returns a new copy.\n",
		ds.LookupName, ds.FuncName, strings.Join(names, " and "), verb)
	if ds.Kind == MasterData && len(keys) == 1 {
		fmt.Fprintf(b, "//\n%s%s\n", keysMarker, ds.Name)
	}

	if len(keys) == 1 {
		fmt.Fprintf(b, "func %s(key %s) (%s, bool) {\n", ds.LookupName, im.TypeString(keys[0].Type()), typ)
		if len(ds.Records) > 0 {
			b.WriteString("switch key {\n")
			for i, r := range ds.Records {
				fmt.Fprintf(b, "case %s:\nreturn %s(), true\n", r.Keys[0], ds.constructor(i))
			}
			b.WriteString("}\n")
		}
	} else {
		params := paramNames(keys, scope)
		var decls []string
		for i, k := range keys {
			decls = append(decls, params[i]+" "+im.TypeString(k.Type()))
		}
		fmt.Fprintf(b, "func %s(%s) (%s, bool) {\n", ds.LookupName, strings.Join(decls, ", "), typ)
		if len(ds.Records) > 0 {
			b.WriteString("switch {\n")
			for i, r := range ds.Records {
				var conds []string
				for j, lit := range r.Keys {
					conds = append(conds, params[j]+" == "+lit)
				}
				fmt.Fprintf(b, "case %s:\nreturn %s(), true\n", strings.Join(conds, " && "), ds.constructor(i))
			}
			b.WriteString("}\n")
		}
	}
	fmt.Fprintf(b, "return %s{}, false\n}\n", typ)
}

// paramNames derives parameter names from key field names, e.g. "UserID" -> "userID".
// Names that would shadow package-level identifiers (such as the schema type, which the
// function body refers to) or that are keywords get a "Key" suffix.
func paramNames(keys []*types.Var, scope *types.Scope) []string {
	var names []string
	for _, k := range keys {
		name := lowerCamel(k.Name())
		for token.IsKeyword(name) || scope.Lookup(name) != nil || strings.HasPrefix(name, "datlas") {
			name += "Key"
		}
		names = append(names, name)
	}
	return names
}

func lowerCamel(s string) string {
	rs := []rune(s)
	for i := range rs {
		// Lower the leading run of upper case letters, keeping the last one of a longer
		// run upper case when it starts the next word ("URLPath" -> "urlPath").
		if !unicode.IsUpper(rs[i]) {
			break
		}
		if i > 0 && i+1 < len(rs) && unicode.IsLower(rs[i+1]) {
			break
		}
		rs[i] = unicode.ToLower(rs[i])
	}
	return string(rs)
}

// renderDatasetOrder renders the function listing the dataset names in dependency order.
func renderDatasetOrder(b *bytes.Buffer, funcName, what string, order []string) {
	var quoted []string
	for _, name := range order {
		quoted = append(quoted, strconv.Quote(name))
	}
	fmt.Fprintf(b, `
// %s returns the names of the %s datasets in dependency order: every dataset comes
// after the datasets it refers to, so applying them in this order stores referenced records first.
// Datasets without an order between them are in name order. Datasets referring to each other
// in a cycle are adjacent, in name order. References of a dataset to itself are not considered.
func %s() []string {
return []string{%s}
}
`, funcName, what, funcName, strings.Join(quoted, ", "))
}

// renderSeedSet renders the environment-level API of seed data.
// The fields of SeedSet follow the dependency order of the datasets.
func renderSeedSet(b *bytes.Buffer, im *output.Imports, datasets []*Dataset, names []string) {
	var (
		envs     []string
		seenEnv  = map[string]bool{}
		seenName = map[string]*Dataset{}
	)
	for _, ds := range datasets {
		if !seenEnv[ds.Env] {
			seenEnv[ds.Env] = true
			envs = append(envs, ds.Env)
		}
		if seenName[ds.Name] == nil {
			seenName[ds.Name] = ds
		}
	}
	sort.Strings(envs)

	var quoted []string
	for _, env := range envs {
		quoted = append(quoted, strconv.Quote(env))
	}
	fmt.Fprintf(b, "\n// %s returns the environments that have seed data, in alphabetical order.\n", seedEnvironmentsName)
	fmt.Fprintf(b, "func %s() []string {\nreturn []string{%s}\n}\n", seedEnvironmentsName, strings.Join(quoted, ", "))

	fmt.Fprintf(b, "\n// %s is the seed data of one environment.\n// Its fields are in the dependency order of %s.\n", seedSetName, seedDatasetsName)
	fmt.Fprintf(b, "type %s struct {\n", seedSetName)
	for _, name := range names {
		ds := seenName[name]
		fmt.Fprintf(b, "// %s is the seed data %q.\n%s []%s\n", naming.Camel(name), name, naming.Camel(name), im.TypeString(ds.Schema.Type.Type()))
	}
	b.WriteString("}\n")

	fmt.Fprintf(b, "\n// %s returns the seed data of the environment env.\n// Each call returns a new copy.\n", seedForName)
	fmt.Fprintf(b, "func %s(env string) (%s, bool) {\nswitch env {\n", seedForName, seedSetName)
	for _, env := range envs {
		fmt.Fprintf(b, "case %q:\nreturn %s{\n", env, seedSetName)
		for _, name := range names {
			for _, ds := range datasets {
				if ds.Env == env && ds.Name == name {
					fmt.Fprintf(b, "%s: %s(),\n", naming.Camel(ds.Name), ds.FuncName)
				}
			}
		}
		b.WriteString("}, true\n")
	}
	fmt.Fprintf(b, "}\nreturn %s{}, false\n}\n", seedSetName)
}
