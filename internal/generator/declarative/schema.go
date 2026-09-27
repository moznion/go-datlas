package declarative

import (
	"errors"
	"fmt"
	"go/types"
	"reflect"
	"strings"

	"github.com/moznion/go-datlas/internal/generator/loader"
)

// ConstraintTagKey is the struct tag key of value constraints, e.g. `datlas:"nonzero"`.
const ConstraintTagKey = "datlas"

// constraints are the parsed form of a `datlas:"..."` struct tag.
type constraints struct {
	nonzero bool
}

func parseConstraints(tag string) (constraints, error) {
	var c constraints
	v, ok := reflect.StructTag(tag).Lookup(ConstraintTagKey)
	if !ok {
		return c, nil
	}
	for _, opt := range strings.Split(v, ",") {
		switch strings.TrimSpace(opt) {
		case "nonzero":
			c.nonzero = true
		case "":
		default:
			return c, fmt.Errorf("unknown %s tag option %q (available: nonzero)", ConstraintTagKey, opt)
		}
	}
	return c, nil
}

// kindTag is the parsed form of the kind specific tag, e.g. `master:"key,ref=roles"`.
type kindTag struct {
	key bool
	ref string
}

func parseKindTag(kind Kind, tag string) (kindTag, bool, error) {
	var kt kindTag
	v, ok := reflect.StructTag(tag).Lookup(kind.tagKey())
	if !ok {
		return kt, false, nil
	}
	for _, opt := range strings.Split(v, ",") {
		opt = strings.TrimSpace(opt)
		switch {
		case opt == "key":
			kt.key = true
		case strings.HasPrefix(opt, "ref="):
			kt.ref = strings.TrimPrefix(opt, "ref=")
			if kt.ref == "" {
				return kt, true, fmt.Errorf("empty reference in %s tag", kind.tagKey())
			}
		default:
			return kt, true, fmt.Errorf("unknown %s tag option %q (available: key, ref=<dataset>)", kind.tagKey(), opt)
		}
	}
	return kt, true, nil
}

// yamlTag is the parsed form of a `yaml:"..."` struct tag.
type yamlTag struct {
	name      string
	skip      bool
	omitempty bool
	inline    bool
}

func parseYAMLTag(tag string) yamlTag {
	v, ok := reflect.StructTag(tag).Lookup("yaml")
	if !ok {
		return yamlTag{}
	}
	if v == "-" {
		return yamlTag{skip: true}
	}
	parts := strings.Split(v, ",")
	t := yamlTag{name: parts[0]}
	for _, opt := range parts[1:] {
		switch opt {
		case "omitempty":
			t.omitempty = true
		case "inline":
			t.inline = true
		}
	}
	return t
}

// structField is a struct field as seen from YAML.
type structField struct {
	v           *types.Var
	rawTag      string
	yamlName    string
	tag         yamlTag
	constraints constraints
	// constraintErr reports a malformed `datlas` tag; checkSupported surfaces it.
	constraintErr error
}

// required reports whether the field must appear in YAML.
// Every field is required unless it is a pointer or tagged with omitempty.
func (f structField) required() bool {
	if f.tag.omitempty {
		return false
	}
	_, isPtr := types.Unalias(f.v.Type()).(*types.Pointer)
	return !isPtr
}

// yamlFields lists the fields of st that are mapped from YAML, in declaration order.
// Unexported fields and fields tagged `yaml:"-"` are not mapped.
func yamlFields(st *types.Struct) []structField {
	var fs []structField
	for i := range st.NumFields() {
		v := st.Field(i)
		tag := parseYAMLTag(st.Tag(i))
		if tag.skip || !v.Exported() {
			continue
		}
		name := tag.name
		if name == "" {
			name = strings.ToLower(v.Name())
		}
		c, err := parseConstraints(st.Tag(i))
		fs = append(fs, structField{v: v, rawTag: st.Tag(i), yamlName: name, tag: tag, constraints: c, constraintErr: err})
	}
	return fs
}

func isNamed(t types.Type, pkg, name string) bool {
	n, ok := types.Unalias(t).(*types.Named)
	return ok && n.Obj().Pkg() != nil && n.Obj().Pkg().Path() == pkg && n.Obj().Name() == name
}

func isTime(t types.Type) bool     { return isNamed(t, "time", "Time") }
func isDuration(t types.Type) bool { return isNamed(t, "time", "Duration") }

// textUnmarshaler is encoding.TextUnmarshaler.
var textUnmarshaler = types.NewInterfaceType([]*types.Func{
	types.NewFunc(0, nil, "UnmarshalText", types.NewSignatureType(nil, nil, nil,
		types.NewTuple(types.NewVar(0, nil, "text", types.NewSlice(types.Typ[types.Byte]))),
		types.NewTuple(types.NewVar(0, nil, "", types.Universe.Lookup("error").Type())), false)),
}, nil).Complete()

// isText reports whether values of t are decoded with encoding.TextUnmarshaler.
// time.Time implements it too but is compiled natively, and //datlas:enum types are
// compiled as literals validated against their constants.
func isText(t types.Type, enums *loader.Enums) bool {
	t = types.Unalias(t)
	if isTime(t) || isEnum(t, enums) {
		return false
	}
	if _, ok := t.(*types.Named); !ok {
		return false
	}
	return types.Implements(types.NewPointer(t), textUnmarshaler)
}

// isEnum reports whether t is declared with //datlas:enum.
// An enum without constants still counts; using it reports that problem.
func isEnum(t types.Type, enums *loader.Enums) bool {
	if enums == nil {
		return false
	}
	_, ok, err := enums.Lookup(t)
	return ok || err != nil
}

// isBytes reports whether t is a byte slice, which is written as a string in YAML.
func isBytes(t types.Type) bool {
	s, ok := types.Unalias(t).Underlying().(*types.Slice)
	if !ok {
		return false
	}
	b, ok := types.Unalias(s.Elem()).(*types.Basic)
	return ok && b.Kind() == types.Uint8
}

// nonzeroSupported reports whether the nonzero constraint can be checked for t.
func nonzeroSupported(t types.Type, enums *loader.Enums) bool {
	t = types.Unalias(t)
	if isTime(t) || isDuration(t) {
		return true
	}
	if isText(t, enums) {
		return false
	}
	switch t.Underlying().(type) {
	case *types.Basic, *types.Pointer, *types.Slice, *types.Map:
		return true
	}
	return false
}

// checkSupported verifies that values of t can be compiled from YAML into Go literals.
// It catches unsupported schemas even when no YAML record exercises them.
func checkSupported(t types.Type, path string, enums *loader.Enums) error {
	return (&supportChecker{seen: map[types.Type]bool{}, enums: enums}).check(t, path)
}

type supportChecker struct {
	seen  map[types.Type]bool
	enums *loader.Enums
}

func (sc *supportChecker) check(t types.Type, path string) error {
	t = types.Unalias(t)
	if isTime(t) || isDuration(t) {
		return nil
	}
	if isText(t, sc.enums) {
		return nil
	}
	if sc.seen[t] {
		return nil
	}
	sc.seen[t] = true

	switch u := t.Underlying().(type) {
	case *types.Basic:
		if u.Info()&(types.IsString|types.IsInteger|types.IsFloat|types.IsBoolean) == 0 || u.Kind() == types.UnsafePointer {
			return fmt.Errorf("%s: unsupported type %s", path, t)
		}
		return nil
	case *types.Pointer:
		return sc.check(u.Elem(), path)
	case *types.Slice:
		return sc.check(u.Elem(), path+"[]")
	case *types.Array:
		return sc.check(u.Elem(), path+"[]")
	case *types.Map:
		kb, ok := u.Key().Underlying().(*types.Basic)
		if !ok || kb.Info()&(types.IsString|types.IsInteger|types.IsBoolean) == 0 {
			return fmt.Errorf("%s: unsupported map key type %s (string, integer or bool types are supported)", path, u.Key())
		}
		return sc.check(u.Elem(), path+"{}")
	case *types.Struct:
		return sc.checkStruct(u, path, map[string]string{})
	default:
		return fmt.Errorf("%s: unsupported type %s", path, t)
	}
}

// checkStruct checks the fields of st; names collects the YAML names in use,
// which inline structs share with their parent.
func (sc *supportChecker) checkStruct(st *types.Struct, path string, names map[string]string) error {
	var errs []error
	for _, f := range yamlFields(st) {
		fpath := path + "." + f.v.Name()
		if f.constraintErr != nil {
			errs = append(errs, fmt.Errorf("%s: %w", fpath, f.constraintErr))
			continue
		}
		if f.constraints.nonzero && !nonzeroSupported(f.v.Type(), sc.enums) {
			errs = append(errs, fmt.Errorf("%s: nonzero is not supported for type %s", fpath, f.v.Type()))
			continue
		}
		if f.tag.inline {
			inner, ok := types.Unalias(f.v.Type()).Underlying().(*types.Struct)
			if _, isPtr := types.Unalias(f.v.Type()).(*types.Pointer); isPtr || !ok {
				errs = append(errs, fmt.Errorf("%s: inline is only supported on struct (non-pointer) fields", fpath))
				continue
			}
			if isTime(f.v.Type()) {
				errs = append(errs, fmt.Errorf("%s: time.Time cannot be inlined", fpath))
				continue
			}
			errs = append(errs, sc.checkStruct(inner, fpath, names))
			continue
		}
		if prev, dup := names[f.yamlName]; dup {
			errs = append(errs, fmt.Errorf("%s: YAML key %q is also used by %s", fpath, f.yamlName, prev))
			continue
		}
		names[f.yamlName] = fpath
		errs = append(errs, sc.check(f.v.Type(), fpath))
	}
	return errors.Join(errs...)
}
