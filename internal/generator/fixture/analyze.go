package fixture

import (
	"errors"
	"fmt"
	"go/token"
	"go/types"
	"math"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/tools/go/packages"

	"github.com/moznion/go-datlas/internal/generator/loader"
	"github.com/moznion/go-datlas/internal/generator/naming"
)

// TagKey is the struct tag key that configures field generation.
const TagKey = "fixture"

// FixtureType is the intermediate representation of a type annotated with //datlas:fixture.
type FixtureType struct {
	// Name is the name of the factory method, e.g. "User".
	Name string
	// PluralName is the name of the method building several values, e.g. "Users".
	PluralName string
	Type       *types.TypeName
	Fields     []Field
}

// Field is a struct field whose value is generated.
// Fields without a fixture tag are left at their zero value and are not listed,
// except embedded structs whose own fields are generated (see Children).
type Field struct {
	Name string
	// Var is the struct field; it is nil for embedded structs listed with Children.
	Var   *types.Var
	Type  types.Type
	Shape Shape
	// Policy describes how the value of the field is generated.
	Policy GeneratorPolicy
	// Children, when not nil, are the generated fields of an untagged embedded struct.
	Children []Field
	// Promoted is true when the embedded struct cannot be named by the generated package
	// (an unexported type of another package); its fields are then set through promotion.
	Promoted bool
}

// Shape describes how the generated value is wrapped to fit the field type.
type Shape struct {
	// Slice is true for []T and []*T fields; Count values are generated.
	Slice bool
	Count int
	// Ptr is true for *T and []*T fields.
	Ptr bool
	// Elem is T.
	Elem types.Type
}

// GeneratorPolicy is the parsed form of a fixture struct tag such as `fixture:"int,min=1,max=10"`.
type GeneratorPolicy struct {
	Generator string
	// Candidates are the values given as "oneof=a|b|c", or the constants of an enum.
	Candidates []string
	// Literals are Go literals of the candidates, set for the enum generator.
	Literals []string
	Params   map[string]string
	// Wrap, when not nil, is applied to the generated value to build the field.
	Wrap *Wrap
	// Unique is set by the "unique" flag: values are distinct within the factory for the type and field.
	Unique bool
}

// Wrap is a function that turns a generated value into the value of a field,
// given with the wrap parameter, e.g. `fixture:"sentence,wrap=example.com/optional.Some"`.
type Wrap struct {
	Func *types.Func
	// TypeArgs instantiate a generic function; they are taken from the field type.
	TypeArgs []types.Type
}

// target is a type a factory is generated for.
// It comes from a //datlas:fixture directive or from an entry of a policy file.
type target struct {
	Pos     token.Position
	Package *packages.Package
	Type    *types.TypeName
	Struct  *types.Struct
	// Name and Plural rename the factory methods; empty means the default.
	Name   string
	Plural string
	// Owner names the target in error messages.
	Owner string
	// Rename tells how to rename the factory methods, for error messages.
	Rename string
	// Policies maps field names to their policy for targets of a policy file;
	// it is nil for directives, whose struct tags are read instead.
	Policies map[string]string
	FieldPos map[string]token.Position
}

// policy returns the fixture policy of field i of st.
func (t *target) policy(st *types.Struct, i int) (string, bool) {
	if t.Policies != nil {
		if st != t.Struct {
			return "", false
		}
		p, ok := t.Policies[st.Field(i).Name()]
		return p, ok
	}
	return reflect.StructTag(st.Tag(i)).Lookup(TagKey)
}

// fieldPos returns the position reported for errors about the field name.
func (t *target) fieldPos(name string) token.Position {
	if pos, ok := t.FieldPos[name]; ok {
		return pos
	}
	return t.Pos
}

// directiveTarget turns a //datlas:fixture directive into a target.
func directiveTarget(d loader.Directive) (target, error) {
	opts, err := parseOptions(d.Args, "name", "plural")
	if err != nil {
		return target{}, fmt.Errorf("%s: //datlas:fixture: %w", d.Pos, err)
	}
	for _, n := range opts {
		if !naming.IsIdentifier(n) || token.IsKeyword(n) {
			return target{}, fmt.Errorf("%s: //datlas:fixture: %q is not a valid Go identifier", d.Pos, n)
		}
	}
	return target{
		Pos:     d.Pos,
		Package: d.Package,
		Type:    d.Type,
		Struct:  d.Struct,
		Name:    opts["name"],
		Plural:  opts["plural"],
		Owner:   d.Type.Pkg().Path() + "." + d.Type.Name(),
		Rename:  "rename it with //datlas:fixture name=...",
	}, nil
}

// analyzer holds what is needed to analyze fixture types.
type analyzer struct {
	outPkgPath string
	enums      *loader.Enums
	// fixtures counts the factories of each type.
	fixtures map[*types.TypeName]int
	// packages are the loaded packages and their dependencies, by import path.
	packages map[string]*types.Package
}

// analyze turns fixture targets into the intermediate representation.
// outPkgPath is the import path of the package the factory is generated into.
// reserved maps method names that must not be generated to what declares them.
// pkgs are the loaded packages, where wrap functions are looked up.
func analyze(targets []target, outPkgPath string, enums *loader.Enums, reserved map[string]string, pkgs []*packages.Package) ([]FixtureType, error) {
	a := &analyzer{
		outPkgPath: outPkgPath,
		enums:      enums,
		fixtures:   map[*types.TypeName]int{},
		packages:   map[string]*types.Package{},
	}
	for _, t := range targets {
		a.fixtures[t.Type]++
	}
	var addPkg func(p *types.Package)
	addPkg = func(p *types.Package) {
		if _, ok := a.packages[p.Path()]; ok {
			return
		}
		a.packages[p.Path()] = p
		for _, imp := range p.Imports() {
			addPkg(imp)
		}
	}
	for _, p := range pkgs {
		if p.Types != nil {
			addPkg(p.Types)
		}
	}

	var (
		result []FixtureType
		errs   []error
		names  = map[string]string{}
	)
	claim := func(name string, t target) error {
		if prev, ok := names[name]; ok {
			return fmt.Errorf("factory method %s for %s conflicts with %s (%s)", name, t.Owner, prev, t.Rename)
		}
		names[name] = t.Owner
		return nil
	}
	for _, name := range []string{"Faker", "Seed"} {
		names[name] = "a method generated by datlas"
	}
	for name, owner := range reserved {
		names[name] = owner
	}

	for _, t := range targets {
		samePkg := t.Type.Pkg().Path() == outPkgPath
		if !samePkg && !t.Type.Exported() {
			errs = append(errs, fmt.Errorf("%s: %s must be exported to be used from package %s", t.Pos, t.Type.Name(), outPkgPath))
			continue
		}
		if t.Package.Name == "main" && !samePkg {
			errs = append(errs, fmt.Errorf("%s: %s is declared in a main package, which cannot be imported", t.Pos, t.Type.Name()))
			continue
		}

		ft := FixtureType{Name: t.Type.Name(), Type: t.Type}
		if t.Name != "" {
			ft.Name = t.Name
		}
		ft.PluralName = naming.Plural(ft.Name)
		if t.Plural != "" {
			ft.PluralName = t.Plural
		}
		if ft.PluralName == ft.Name {
			ft.PluralName = ft.Name + "List"
		}

		if t.Policies != nil {
			if perrs := a.checkPolicyFields(t, samePkg); len(perrs) > 0 {
				errs = append(errs, perrs...)
				continue
			}
		}
		fields, ferrs := a.fields(&t, t.Struct, t.Type.Name(), samePkg)
		if len(ferrs) > 0 {
			errs = append(errs, ferrs...)
			continue
		}
		ft.Fields = fields
		for _, n := range []string{ft.Name, ft.PluralName} {
			if err := claim(n, t); err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", t.Pos, err))
			}
		}
		result = append(result, ft)
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	if err := checkCycles(result); err != nil {
		return nil, err
	}
	return result, nil
}

// checkPolicyFields verifies that a policy file entry mentions exactly the fields
// the generated package can set, so that fields added to the type later are not missed.
func (a *analyzer) checkPolicyFields(t target, samePkg bool) []error {
	var errs []error
	fields := map[string]*types.Var{}
	for i := range t.Struct.NumFields() {
		v := t.Struct.Field(i)
		fields[v.Name()] = v
		if v.Name() == "_" || (!samePkg && !v.Exported()) {
			continue
		}
		if _, ok := t.Policies[v.Name()]; !ok {
			errs = append(errs, fmt.Errorf("%s: %s.%s has no policy; give it one, or \"-\" to leave it at its zero value", t.Pos, t.Type.Name(), v.Name()))
		}
	}
	names := make([]string, 0, len(t.Policies))
	for name := range t.Policies {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if _, ok := fields[name]; !ok || name == "_" {
			errs = append(errs, fmt.Errorf("%s: %s has no field %s", t.fieldPos(name), t.Type.Name(), name))
		}
	}
	return errs
}

// parseOptions parses directive arguments of the form key=value.
func parseOptions(args []string, allowed ...string) (map[string]string, error) {
	opts := map[string]string{}
	for _, arg := range args {
		k, v, ok := strings.Cut(arg, "=")
		if !ok || v == "" || !slices.Contains(allowed, k) {
			return nil, fmt.Errorf("unknown option %q (available: %s)", arg, strings.Join(allowed, "=..., ")+"=...")
		}
		if _, dup := opts[k]; dup {
			return nil, fmt.Errorf("duplicate option %q", k)
		}
		opts[k] = v
	}
	return opts, nil
}

// fields analyzes the fields of st; path names st in error messages.
func (a *analyzer) fields(t *target, st *types.Struct, path string, samePkg bool) ([]Field, []error) {
	var (
		fields []Field
		errs   []error
	)
	for i := range st.NumFields() {
		v := st.Field(i)
		ctx := fmt.Sprintf("%s: %s.%s", t.fieldPos(v.Name()), path, v.Name())
		tag, tagged := t.policy(st, i)
		if tag == "-" {
			continue
		}
		if !tagged {
			if t.Policies != nil || !v.Embedded() || isTime(v.Type()) {
				continue
			}
			// Untagged embedded structs are walked so that their tagged fields are generated too.
			if ptr, ok := types.Unalias(v.Type()).(*types.Pointer); ok {
				if inner, ok := ptr.Elem().Underlying().(*types.Struct); ok && hasFixtureTags(inner, map[*types.Struct]bool{}) {
					errs = append(errs, fmt.Errorf("%s: embedded pointer %s has fixture tags but is not generated; tag it with fixture:\"nested\" (for a //datlas:fixture type) or fixture:\"-\"", ctx, v.Type()))
				}
				continue
			}
			inner, ok := types.Unalias(v.Type()).Underlying().(*types.Struct)
			if !ok {
				continue
			}
			promoted := !samePkg && !isNameable(v.Type(), a.outPkgPath)
			children, cerrs := a.fields(t, inner, path+"."+v.Name(), samePkg)
			if len(cerrs) > 0 {
				errs = append(errs, cerrs...)
				continue
			}
			if len(children) == 0 {
				continue
			}
			if promoted {
				// Promoted fields are set as v.Name, which must resolve to the field itself.
				for _, leaf := range leaves(children) {
					obj, _, _ := types.LookupFieldOrMethod(t.Type.Type(), false, t.Type.Pkg(), leaf.Name)
					if obj != leaf.Var {
						errs = append(errs, fmt.Errorf("%s.%s: the field is embedded through the unexported type %s and is shadowed by another field of the same name, so it cannot be set", ctx, leaf.Name, v.Type()))
					}
				}
			}
			fields = append(fields, Field{Name: v.Name(), Type: v.Type(), Children: children, Promoted: promoted})
			continue
		}
		if err := loader.CheckType(t.Package, v.Type(), ctx); err != nil {
			errs = append(errs, err)
			continue
		}
		if err := a.checkAccessible(v, samePkg); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", ctx, err))
			continue
		}
		policy, err := parseTag(tag)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", ctx, err))
			continue
		}
		if v.Embedded() && policy.Generator != "nested" {
			errs = append(errs, fmt.Errorf("%s: embedded fields only support the \"nested\" generator", ctx))
			continue
		}
		var shape Shape
		if ref, ok := policy.Params["wrap"]; ok {
			// The wrap function builds the whole field, so the field is not decomposed into
			// slices or pointers: wrapper types such as go-optional's Option are slices themselves.
			if _, ok := policy.Params["count"]; ok {
				errs = append(errs, fmt.Errorf("%s: parameter \"count\" cannot be combined with \"wrap\"", ctx))
				continue
			}
			policy.Wrap, shape.Elem, err = a.resolveWrap(ref, v.Type())
		} else {
			shape, err = shapeOf(v.Type(), policy)
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", ctx, err))
			continue
		}
		if policy, err = a.validate(policy, shape.Elem); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", ctx, err))
			continue
		}
		fields = append(fields, Field{Name: v.Name(), Var: v, Type: v.Type(), Shape: shape, Policy: policy})
	}
	return fields, errs
}

// hasFixtureTags reports whether st or a struct embedded in it has fixture tags.
func hasFixtureTags(st *types.Struct, seen map[*types.Struct]bool) bool {
	if seen[st] {
		return false
	}
	seen[st] = true
	for i := range st.NumFields() {
		if tag, ok := reflect.StructTag(st.Tag(i)).Lookup(TagKey); ok && tag != "-" {
			return true
		}
		if f := st.Field(i); f.Embedded() {
			if inner, ok := f.Type().Underlying().(*types.Struct); ok && hasFixtureTags(inner, seen) {
				return true
			}
		}
	}
	return false
}

// isNameable reports whether the generated package can refer to the named type t.
func isNameable(t types.Type, outPkgPath string) bool {
	n, ok := types.Unalias(t).(*types.Named)
	return !ok || n.Obj().Exported() || n.Obj().Pkg() == nil || n.Obj().Pkg().Path() == outPkgPath
}

// leaves lists the generated fields below fields, looking through embedded structs.
func leaves(fields []Field) []Field {
	var ls []Field
	for _, f := range fields {
		if f.Children != nil {
			ls = append(ls, leaves(f.Children)...)
		} else {
			ls = append(ls, f)
		}
	}
	return ls
}

// checkAccessible verifies that the generated package can set the field and name its type.
func (a *analyzer) checkAccessible(v *types.Var, samePkg bool) error {
	if samePkg {
		return nil
	}
	if !v.Exported() {
		return fmt.Errorf("unexported field cannot be set from package %s", a.outPkgPath)
	}
	var unexported string
	var walk func(t types.Type)
	walk = func(t types.Type) {
		switch t := types.Unalias(t).(type) {
		case *types.Named:
			if obj := t.Obj(); obj.Pkg() != nil && obj.Pkg().Path() != a.outPkgPath && !obj.Exported() && unexported == "" {
				unexported = obj.Name()
			}
		case *types.Pointer:
			walk(t.Elem())
		case *types.Slice:
			walk(t.Elem())
		case *types.Array:
			walk(t.Elem())
		case *types.Map:
			walk(t.Key())
			walk(t.Elem())
		}
	}
	walk(v.Type())
	if unexported != "" {
		return fmt.Errorf("type %s is unexported and cannot be used from package %s", unexported, a.outPkgPath)
	}
	return nil
}

// checkCycles rejects nested fields that would make factories recurse forever.
func checkCycles(fts []FixtureType) error {
	byType := map[*types.TypeName]FixtureType{}
	for _, ft := range fts {
		byType[ft.Type] = ft
	}
	const (
		unvisited = iota
		visiting
		done
	)
	state := map[*types.TypeName]int{}
	var visit func(ft FixtureType, path []string) error
	visit = func(ft FixtureType, path []string) error {
		state[ft.Type] = visiting
		for _, f := range flatten(ft.Fields) {
			if f.Policy.Generator != "nested" {
				continue
			}
			target := types.Unalias(f.Shape.Elem).(*types.Named).Obj()
			next := append(path, ft.Name+"."+f.Name)
			switch state[target] {
			case visiting:
				return fmt.Errorf("nested fixtures form a cycle: %s -> %s", strings.Join(next, " -> "), target.Name())
			case unvisited:
				if err := visit(byType[target], next); err != nil {
					return err
				}
			}
		}
		state[ft.Type] = done
		return nil
	}
	for _, ft := range fts {
		if state[ft.Type] == unvisited {
			if err := visit(ft, nil); err != nil {
				return err
			}
		}
	}
	return nil
}

// flatten lists fields together with the fields of embedded structs.
func flatten(fields []Field) []Field {
	var all []Field
	for _, f := range fields {
		all = append(all, f)
		all = append(all, flatten(f.Children)...)
	}
	return all
}

// splitEscaped splits s at unescaped occurrences of sep and removes the escapes.
// A backslash escapes the following character.
func splitEscaped(s string, sep byte) []string {
	var (
		parts []string
		cur   strings.Builder
	)
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == '\\' && i+1 < len(s):
			i++
			cur.WriteByte(s[i])
		case s[i] == sep:
			parts = append(parts, cur.String())
			cur.Reset()
		default:
			cur.WriteByte(s[i])
		}
	}
	return append(parts, cur.String())
}

// unescape removes the backslash escapes of s.
func unescape(s string) string {
	return strings.Join(splitEscaped(s, ','), ",")
}

// splitRaw splits s at unescaped occurrences of sep, keeping the escapes.
func splitRaw(s string, sep byte) []string {
	var parts []string
	start := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case sep:
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	return append(parts, s[start:])
}

func parseTag(tag string) (GeneratorPolicy, error) {
	parts := splitRaw(tag, ',')
	p := GeneratorPolicy{Params: map[string]string{}}
	name, arg, hasArg := strings.Cut(strings.TrimSpace(parts[0]), "=")
	p.Generator = name
	if p.Generator == "" {
		return p, fmt.Errorf("empty fixture tag")
	}
	spec, ok := generators[p.Generator]
	if !ok {
		return p, fmt.Errorf("unknown fixture generator %q (available: %s)", p.Generator, strings.Join(generatorNames(), ", "))
	}
	if spec.needsArg && (!hasArg || arg == "") {
		return p, fmt.Errorf("fixture generator %q requires candidates, e.g. %s=a|b", p.Generator, p.Generator)
	}
	if !spec.needsArg && hasArg {
		return p, fmt.Errorf("fixture generator %q does not take a value", p.Generator)
	}
	if hasArg {
		p.Candidates = splitEscaped(arg, '|')
	}
	for _, kv := range parts[1:] {
		if strings.TrimSpace(kv) == "unique" {
			if p.Unique {
				return p, fmt.Errorf("duplicate fixture tag parameter %q", "unique")
			}
			p.Unique = true
			continue
		}
		k, v, ok := strings.Cut(strings.TrimSpace(kv), "=")
		if !ok || k == "" {
			return p, fmt.Errorf("malformed fixture tag parameter %q: expected key=value", kv)
		}
		if !spec.params[k] && k != "count" && k != "wrap" {
			return p, fmt.Errorf("fixture generator %q does not accept parameter %q", p.Generator, k)
		}
		if _, dup := p.Params[k]; dup {
			return p, fmt.Errorf("duplicate fixture tag parameter %q", k)
		}
		p.Params[k] = unescape(v)
	}
	return p, nil
}

// shapeOf decomposes a field type into T, *T, []T or []*T.
func shapeOf(t types.Type, p GeneratorPolicy) (Shape, error) {
	var s Shape
	if sl, ok := t.Underlying().(*types.Slice); ok {
		s.Slice = true
		t = sl.Elem()
	}
	if ptr, ok := types.Unalias(t).(*types.Pointer); ok {
		s.Ptr = true
		t = ptr.Elem()
	}
	s.Elem = t

	countStr, hasCount := p.Params["count"]
	switch {
	case hasCount && !s.Slice:
		return s, fmt.Errorf("parameter \"count\" is only allowed on slice fields")
	case hasCount:
		n, err := strconv.Atoi(countStr)
		if err != nil || n < 0 {
			return s, fmt.Errorf("invalid count %q: must be a non-negative integer", countStr)
		}
		s.Count = n
	case s.Slice:
		s.Count = 1
	}
	return s, nil
}

// fieldKind classifies the type a generator produces a value for.
type fieldKind int

const (
	kindOther fieldKind = iota
	kindString
	kindSigned
	kindUnsigned
	kindFloat
	kindBool
	kindTime
	kindUUID
	kindFixture
)

// classify returns the kind of the element type t.
func classify(t types.Type) (fieldKind, *types.Basic) {
	if isTime(t) {
		return kindTime, nil
	}
	switch u := t.Underlying().(type) {
	case *types.Basic:
		info := u.Info()
		switch {
		case info&types.IsString != 0:
			return kindString, u
		case info&types.IsInteger != 0 && info&types.IsUnsigned != 0:
			return kindUnsigned, u
		case info&types.IsInteger != 0:
			return kindSigned, u
		case info&types.IsFloat != 0:
			return kindFloat, u
		case info&types.IsBoolean != 0:
			return kindBool, u
		}
	case *types.Array:
		if b, ok := u.Elem().Underlying().(*types.Basic); ok && u.Len() == 16 && b.Kind() == types.Uint8 {
			return kindUUID, nil
		}
	case *types.Struct:
		return kindFixture, nil
	}
	return kindOther, nil
}

func isTime(t types.Type) bool {
	n, ok := types.Unalias(t).(*types.Named)
	return ok && n.Obj().Pkg() != nil && n.Obj().Pkg().Path() == "time" && n.Obj().Name() == "Time"
}

// validate checks that the generator can produce values of elem and completes the policy.
func (a *analyzer) validate(p GeneratorPolicy, elem types.Type) (GeneratorPolicy, error) {
	spec := generators[p.Generator]
	kind, basic := classify(elem)
	if p.Generator == "nested" {
		named, ok := types.Unalias(elem).(*types.Named)
		if !ok || a.fixtures[named.Obj()] == 0 {
			return p, fmt.Errorf("fixture generator \"nested\" requires a //datlas:fixture type or a type of the policy file (or a pointer or slice of one), not %s", elem)
		}
		if n := a.fixtures[named.Obj()]; n > 1 {
			return p, fmt.Errorf("fixture generator \"nested\" cannot choose between the %d factories of %s", n, elem)
		}
		return p, nil
	}
	if !spec.kinds[kind] {
		return p, fmt.Errorf("fixture generator %q cannot produce a value of type %s", p.Generator, elem)
	}
	if p.Unique {
		switch p.Generator {
		case "email", "username", "seq":
			return p, fmt.Errorf("fixture generator %q is always unique; remove \"unique\"", p.Generator)
		case "now", "bool":
			return p, fmt.Errorf("fixture generator %q cannot be combined with \"unique\": it has too few distinct values", p.Generator)
		}
	}
	enum, isEnum, err := a.enums.Lookup(elem)
	if err != nil {
		return p, err
	}
	switch p.Generator {
	case "int":
		_, _, err = intRange(p, kind, basic)
	case "float":
		_, _, err = floatRange(p)
	case "alpha", "alphanum":
		_, err = length(p)
	case "oneof":
		var lits []string
		if lits, err = oneOfLiterals(p, kind, basic); err == nil && isEnum {
			for i, lit := range lits {
				if !enum.Literals[lit] {
					err = fmt.Errorf("oneof candidate %q is not a valid %s (allowed: %s)", p.Candidates[i], elem, strings.Join(enum.Names, ", "))
					break
				}
			}
		}
	case "enum":
		if !isEnum {
			err = fmt.Errorf("fixture generator \"enum\" requires a //datlas:enum type, not %s", elem)
		}
		p.Literals = enum.Values
	}
	return p, err
}

type generatorSpec struct {
	kinds    map[fieldKind]bool
	params   map[string]bool
	needsArg bool
}

func kinds(ks ...fieldKind) map[fieldKind]bool {
	m := map[fieldKind]bool{}
	for _, k := range ks {
		m[k] = true
	}
	return m
}

func params(ps ...string) map[string]bool {
	m := map[string]bool{}
	for _, p := range ps {
		m[p] = true
	}
	return m
}

var generators = map[string]generatorSpec{
	"uuid":       {kinds: kinds(kindUUID, kindString)},
	"email":      {kinds: kinds(kindString)},
	"name":       {kinds: kinds(kindString)},
	"first_name": {kinds: kinds(kindString)},
	"last_name":  {kinds: kinds(kindString)},
	"username":   {kinds: kinds(kindString)},
	"word":       {kinds: kinds(kindString)},
	"sentence":   {kinds: kinds(kindString)},
	"url":        {kinds: kinds(kindString)},
	"phone":      {kinds: kinds(kindString)},
	"alpha":      {kinds: kinds(kindString), params: params("len")},
	"alphanum":   {kinds: kinds(kindString), params: params("len")},
	"int":        {kinds: kinds(kindSigned, kindUnsigned), params: params("min", "max")},
	"float":      {kinds: kinds(kindFloat), params: params("min", "max")},
	"bool":       {kinds: kinds(kindBool)},
	"now":        {kinds: kinds(kindTime)},
	"past":       {kinds: kinds(kindTime)},
	"future":     {kinds: kinds(kindTime)},
	"seq":        {kinds: kinds(kindSigned, kindUnsigned, kindString), params: params("prefix")},
	"oneof":      {kinds: kinds(kindString, kindSigned, kindUnsigned, kindFloat, kindBool), needsArg: true},
	"nested":     {kinds: kinds(kindFixture)},
	"enum":       {kinds: kinds(kindString, kindSigned, kindUnsigned)},
}

func generatorNames() []string {
	names := make([]string, 0, len(generators))
	for n := range generators {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

const (
	defaultIntMax   = 1_000_000
	defaultFloatMax = 1000
	defaultLength   = 16
)

func intRange(p GeneratorPolicy, kind fieldKind, basic *types.Basic) (lo, hi string, err error) {
	bits := basicBits(basic)
	if kind == kindUnsigned {
		max := uint64(math.MaxUint64) >> (64 - bits)
		l, h := uint64(1), min(uint64(defaultIntMax), max)
		if s, ok := p.Params["min"]; ok {
			if l, err = strconv.ParseUint(s, 0, bits); err != nil {
				return "", "", fmt.Errorf("invalid min %q for %s: %w", s, basic, err)
			}
		}
		if s, ok := p.Params["max"]; ok {
			if h, err = strconv.ParseUint(s, 0, bits); err != nil {
				return "", "", fmt.Errorf("invalid max %q for %s: %w", s, basic, err)
			}
		}
		if l > h {
			return "", "", fmt.Errorf("min %d is greater than max %d", l, h)
		}
		return strconv.FormatUint(l, 10), strconv.FormatUint(h, 10), nil
	}
	max := int64(math.MaxInt64) >> (64 - bits)
	l, h := int64(1), min(int64(defaultIntMax), max)
	if s, ok := p.Params["min"]; ok {
		if l, err = strconv.ParseInt(s, 0, bits); err != nil {
			return "", "", fmt.Errorf("invalid min %q for %s: %w", s, basic, err)
		}
	}
	if s, ok := p.Params["max"]; ok {
		if h, err = strconv.ParseInt(s, 0, bits); err != nil {
			return "", "", fmt.Errorf("invalid max %q for %s: %w", s, basic, err)
		}
	}
	if l > h {
		return "", "", fmt.Errorf("min %d is greater than max %d", l, h)
	}
	return strconv.FormatInt(l, 10), strconv.FormatInt(h, 10), nil
}

func floatRange(p GeneratorPolicy) (lo, hi string, err error) {
	l, h := 0.0, float64(defaultFloatMax)
	if s, ok := p.Params["min"]; ok {
		if l, err = strconv.ParseFloat(s, 64); err != nil {
			return "", "", fmt.Errorf("invalid min %q: %w", s, err)
		}
	}
	if s, ok := p.Params["max"]; ok {
		if h, err = strconv.ParseFloat(s, 64); err != nil {
			return "", "", fmt.Errorf("invalid max %q: %w", s, err)
		}
	}
	if l > h {
		return "", "", fmt.Errorf("min %g is greater than max %g", l, h)
	}
	return formatFloat(l), formatFloat(h), nil
}

func length(p GeneratorPolicy) (int, error) {
	s, ok := p.Params["len"]
	if !ok {
		return defaultLength, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("invalid len %q: must be a non-negative integer", s)
	}
	return n, nil
}

// oneOfLiterals returns the Go literals of the candidates of a oneof generator.
func oneOfLiterals(p GeneratorPolicy, kind fieldKind, basic *types.Basic) ([]string, error) {
	var lits []string
	for _, s := range p.Candidates {
		switch kind {
		case kindString:
			lits = append(lits, strconv.Quote(s))
		case kindSigned:
			v, err := strconv.ParseInt(s, 0, basicBits(basic))
			if err != nil {
				return nil, fmt.Errorf("invalid oneof candidate %q for %s: %w", s, basic, err)
			}
			lits = append(lits, strconv.FormatInt(v, 10))
		case kindUnsigned:
			v, err := strconv.ParseUint(s, 0, basicBits(basic))
			if err != nil {
				return nil, fmt.Errorf("invalid oneof candidate %q for %s: %w", s, basic, err)
			}
			lits = append(lits, strconv.FormatUint(v, 10))
		case kindFloat:
			v, err := strconv.ParseFloat(s, basicBits(basic))
			if err != nil {
				return nil, fmt.Errorf("invalid oneof candidate %q for %s: %w", s, basic, err)
			}
			lits = append(lits, formatFloat(v))
		case kindBool:
			v, err := strconv.ParseBool(s)
			if err != nil {
				return nil, fmt.Errorf("invalid oneof candidate %q for %s: %w", s, basic, err)
			}
			lits = append(lits, strconv.FormatBool(v))
		}
	}
	return lits, nil
}

func formatFloat(f float64) string {
	s := strconv.FormatFloat(f, 'g', -1, 64)
	if !strings.ContainsAny(s, ".eE") {
		s += ".0"
	}
	return s
}

// basicBits returns the size in bits of a numeric basic type.
func basicBits(b *types.Basic) int {
	switch b.Kind() {
	case types.Int8, types.Uint8:
		return 8
	case types.Int16, types.Uint16:
		return 16
	case types.Int32, types.Uint32, types.Float32:
		return 32
	default:
		// int, uint and uintptr are treated as 64-bit, which is what generated code targets in practice.
		return 64
	}
}
