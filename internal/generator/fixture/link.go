package fixture

import (
	"fmt"
	"go/types"
	"sort"
	"strings"
)

// From is the source of fixture:"from=PATH": the field is copied from another field of the same value.
type From struct {
	// Path is the selector of the source relative to the value, e.g. "Company.ID".
	Path string
	Type types.Type
	// First is the field named by the first element of Path.
	First *types.Var
}

// RefLink is the parameter ref=FACTORY.PATH: an option function setting the field from a value
// built by another factory, e.g. UserCompanyIDFrom(c) sets CompanyID to c.ID.
type RefLink struct {
	// Factory is the name of the factory method building the source, e.g. "Company".
	Factory string
	Source  *types.TypeName
	Path    string
	Type    types.Type
	// Target is the type the source value is converted to before wrap and Ptr are applied.
	Target types.Type
	Ptr    bool
}

// resolvePath resolves a dotted field path such as "Company.ID" on the struct type root.
// It returns the type of the last field and the field named by the first element.
// Paths must not go through pointers, which may be nil.
func (a *analyzer) resolvePath(root types.Type, path string) (types.Type, *types.Var, error) {
	if path == "" {
		return nil, nil, fmt.Errorf("empty field path")
	}
	var (
		cur   = root
		first *types.Var
	)
	for i, name := range strings.Split(path, ".") {
		if _, ok := types.Unalias(cur).(*types.Pointer); ok {
			return nil, nil, fmt.Errorf("field path %s goes through the pointer %s, which may be nil", path, cur)
		}
		var pkg *types.Package
		if n, ok := types.Unalias(cur).(*types.Named); ok {
			pkg = n.Obj().Pkg()
		}
		obj, _, indirect := types.LookupFieldOrMethod(cur, false, pkg, name)
		v, ok := obj.(*types.Var)
		if !ok || !v.IsField() {
			return nil, nil, fmt.Errorf("%s has no field %s (in field path %s)", cur, name, path)
		}
		if indirect {
			return nil, nil, fmt.Errorf("field path %s goes through an embedded pointer, which may be nil", path)
		}
		if !v.Exported() && (v.Pkg() == nil || v.Pkg().Path() != a.outPkgPath) {
			return nil, nil, fmt.Errorf("field %s of field path %s is unexported and cannot be read from package %s", name, path, a.outPkgPath)
		}
		if i == 0 {
			first = v
		}
		cur = v.Type()
	}
	return cur, first, nil
}

// linkShape decides how a value of type src is assigned to a field of type field:
// directly, or through Ptr for a pointer field.
func linkShape(src, field types.Type) (Shape, error) {
	if types.AssignableTo(src, field) {
		return Shape{Elem: field}, nil
	}
	if ptr, ok := types.Unalias(field).(*types.Pointer); ok && types.AssignableTo(src, ptr.Elem()) {
		return Shape{Ptr: true, Elem: ptr.Elem()}, nil
	}
	return Shape{}, fmt.Errorf("%s cannot be assigned to %s", src, field)
}

// refOption is an option function generated for a field with the ref parameter.
type refOption struct {
	Name  string
	Field Field
}

// refOptions lists the option functions generated for the fields of ft.
func (ft FixtureType) refOptions() []refOption {
	var opts []refOption
	for _, f := range leaves(ft.Fields) {
		if f.Policy.Ref != nil {
			opts = append(opts, refOption{Name: ft.Name + f.Name + "From", Field: f})
		}
	}
	return opts
}

// resolveRef resolves the parameter ref=FACTORY.PATH of a field of type field whose value is
// generated with policy and shape.
func (a *analyzer) resolveRef(ref string, policy GeneratorPolicy, shape Shape, field types.Type) (*RefLink, error) {
	factory, path, ok := strings.Cut(ref, ".")
	if !ok || path == "" {
		return nil, fmt.Errorf("parameter \"ref\" takes a factory and a field path, e.g. ref=Company.ID")
	}
	source := a.factories[factory]
	if source == nil {
		var names []string
		for n := range a.factories {
			names = append(names, n)
		}
		sort.Strings(names)
		return nil, fmt.Errorf("ref=%s: no factory method %s (available: %s)", ref, factory, strings.Join(names, ", "))
	}
	typ, _, err := a.resolvePath(source.Type(), path)
	if err != nil {
		return nil, fmt.Errorf("ref=%s: %w", ref, err)
	}
	link := &RefLink{Factory: factory, Source: source, Path: path, Type: typ}
	if policy.Wrap != nil {
		// The wrap function takes the source value, as it takes the generated one.
		if !types.AssignableTo(typ, shape.Elem) {
			return nil, fmt.Errorf("ref=%s: field %s is %s, which cannot be passed to the wrap function taking %s", ref, path, typ, shape.Elem)
		}
		link.Target = shape.Elem
		return link, nil
	}
	s, err := linkShape(typ, field)
	if err != nil {
		return nil, fmt.Errorf("ref=%s: field %s is %s, which cannot be assigned to %s", ref, path, typ, field)
	}
	link.Target, link.Ptr = s.Elem, s.Ptr
	return link, nil
}

// checkFromChains rejects from fields copying a field that is itself copied with from,
// whose value would depend on the order of the assignments.
func checkFromChains(t target, fields []Field) []error {
	copied := map[*types.Var]bool{}
	for _, f := range leaves(fields) {
		if f.Policy.From != nil {
			copied[f.Var] = true
		}
	}
	var errs []error
	for _, f := range leaves(fields) {
		if p := f.Policy.From; p != nil && copied[p.First] {
			errs = append(errs, fmt.Errorf("%s: %s.%s: from=%s copies a field that is itself set with from; copy from its source instead",
				t.fieldPos(f.Name), t.Type.Name(), f.Name, p.Path))
		}
	}
	return errs
}
