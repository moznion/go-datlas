package fixture

import (
	"fmt"
	"go/types"
)

// resolveWrap resolves the wrap function ref for a field of type field.
// It returns the function and the type of the value it takes, which the generator produces.
//
// ref is "import/path.Func", or "Func" for a function of the output package.
// The function must be func(X) W with W assignable to field. A generic function is
// instantiated with the type arguments of field, e.g. Some[T] with those of Option[string].
func (a *analyzer) resolveWrap(ref string, field types.Type) (*Wrap, types.Type, error) {
	pkgPath, name := splitQualified(ref)
	if pkgPath == "" {
		pkgPath = a.outPkgPath
	}
	pkg := a.packages[pkgPath]
	if pkg == nil {
		if pkgPath == a.outPkgPath {
			return nil, nil, fmt.Errorf("wrap function %s is not declared in the output package %s", name, pkgPath)
		}
		return nil, nil, fmt.Errorf("package %s of wrap function %s is not loaded; list it in PACKAGES, or declare the function in the output package", pkgPath, name)
	}
	fn, ok := pkg.Scope().Lookup(name).(*types.Func)
	switch {
	case !ok && pkgPath == a.outPkgPath:
		return nil, nil, fmt.Errorf("wrap function %s is not declared in the output package %s", name, pkgPath)
	case !ok:
		return nil, nil, fmt.Errorf("wrap function %s is not declared in package %s", name, pkgPath)
	}
	if pkgPath != a.outPkgPath && !fn.Exported() {
		return nil, nil, fmt.Errorf("wrap function %s.%s is unexported and cannot be used from package %s", pkgPath, name, a.outPkgPath)
	}
	sig := fn.Signature()
	if sig.Params().Len() != 1 || sig.Results().Len() != 1 || sig.Variadic() {
		return nil, nil, fmt.Errorf("wrap function %s must take one value and return one value, not %s", ref, sig)
	}
	w := &Wrap{Func: fn}
	if tparams := sig.TypeParams(); tparams.Len() > 0 {
		named, ok := types.Unalias(field).(*types.Named)
		if !ok || named.TypeArgs().Len() != tparams.Len() {
			return nil, nil, fmt.Errorf("cannot infer the type arguments of the generic wrap function %s from %s", ref, field)
		}
		for i := range named.TypeArgs().Len() {
			w.TypeArgs = append(w.TypeArgs, named.TypeArgs().At(i))
		}
		inst, err := types.Instantiate(nil, sig, w.TypeArgs, true)
		if err != nil {
			return nil, nil, fmt.Errorf("cannot instantiate the wrap function %s for %s: %w", ref, field, err)
		}
		sig = inst.(*types.Signature)
	}
	if out := sig.Results().At(0).Type(); !types.AssignableTo(out, field) {
		return nil, nil, fmt.Errorf("wrap function %s returns %s, which cannot be assigned to %s", ref, out, field)
	}
	return w, sig.Params().At(0).Type(), nil
}
