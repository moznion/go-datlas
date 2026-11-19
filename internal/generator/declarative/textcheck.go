package declarative

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"go/types"

	"golang.org/x/tools/go/packages"

	"github.com/moznion/go-datlas/internal/generator/loader"
	"github.com/moznion/go-datlas/internal/generator/output"
)

// runTextChecks validates the values of types decoded with encoding.TextUnmarshaler.
//
// It also checks the logical keys of such types, which different texts can denote
// (e.g. a UUID in upper or lower case): duplicate keys and references to them are found by
// comparing decoded values, with the messages of checkDuplicateKeys and checkRefs.
//
// The generator cannot run the user's UnmarshalText methods itself. Instead it adds, through a
// build overlay, a function to the schema package that decodes every value (so unexported types
// work too), and builds and runs a temporary program calling it. The overlay also replaces the
// generated file by its new content, so a stale or missing generated file does not matter.
// Nothing is written to the package except a temporary directory, removed afterwards.
func runTextChecks(pkg *packages.Package, pkgDir, genPath string, genSrc []byte, checks []textCheck, datasets []*Dataset, enums *loader.Enums) error {
	if len(checks) == 0 {
		return nil
	}
	if pkg.Name == "main" {
		return fmt.Errorf("%s: values of types implementing encoding.TextUnmarshaler cannot be validated in a main package; move the schema to another package", checks[0].pos)
	}
	tmp, err := os.MkdirTemp(pkgDir, ".datlas-check-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	scope := pkg.Types.Scope()
	checkFunc := "DatlasTextCheck"
	for i := 2; scope.Lookup(checkFunc) != nil || scope.Lookup(checkFunc+"Decode") != nil; i++ {
		checkFunc = "DatlasTextCheck" + strconv.Itoa(i)
	}
	decodeFunc := checkFunc + "Decode"

	// The function added to the schema package.
	im := output.NewImports(pkg.PkgPath, scope.Names()...)
	fmtPkg := im.Add("fmt", "fmt")
	encPkg := im.Add("encoding", "encoding")
	var body bytes.Buffer
	fmt.Fprintf(&body, `// %[1]s reports the values that fail to decode. It exists only while datlas validates them.
func %[1]s() []string {
	var datlasErrs []string
	datlasCheck := func(pos, typ string, u %[2]s.TextUnmarshaler, text string) {
		if err := u.UnmarshalText([]byte(text)); err != nil {
			datlasErrs = append(datlasErrs, %[3]s.Sprintf("%%s: invalid %%s %%q: %%v", pos, typ, text, err))
		}
	}
`, checkFunc, encPkg, fmtPkg)
	for _, c := range checks {
		fmt.Fprintf(&body, "datlasCheck(%s, %s, new(%s), %s)\n",
			strconv.Quote(c.pos), strconv.Quote(typeName(c.typ)), im.TypeString(c.typ), strconv.Quote(c.text))
	}
	renderTextKeyChecks(&body, im, fmtPkg, decodeFunc, datasets, enums)
	body.WriteString("return datlasErrs\n}\n")
	fmt.Fprintf(&body, `
// %s decodes s, reporting whether it is valid. It exists only while datlas validates values.
func %s[T any, PT interface {
	*T
	UnmarshalText([]byte) error
}](s string) (T, bool) {
	var v T
	err := PT(&v).UnmarshalText([]byte(s))
	return v, err == nil
}
`, decodeFunc, decodeFunc)

	// The program calling it.
	mainIm := output.NewImports("main")
	target := mainIm.Add(pkg.PkgPath, pkg.Name)
	mainFmt := mainIm.Add("fmt", "fmt")
	mainOS := mainIm.Add("os", "os")
	mainBody := fmt.Sprintf(`func main() {
	errs := %[1]s.%[2]s()
	for _, e := range errs {
		%[3]s.Println(e)
	}
	if len(errs) > 0 {
		%[4]s.Exit(3)
	}
}
`, target, checkFunc, mainFmt, mainOS)

	files := map[string][]byte{
		"generated.go.txt": genSrc,
		"check.go.txt":     output.File(pkg.Name, im, body.Bytes()),
		"main.go":          output.File("main", mainIm, []byte(mainBody)),
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(tmp, name), content, 0o644); err != nil {
			return err
		}
	}
	overlay, err := json.Marshal(map[string]map[string]string{"Replace": {
		genPath: filepath.Join(tmp, "generated.go.txt"),
		filepath.Join(pkgDir, "zz_datlas_textcheck.go"): filepath.Join(tmp, "check.go.txt"),
	}})
	if err != nil {
		return err
	}
	overlayPath := filepath.Join(tmp, "overlay.json")
	if err := os.WriteFile(overlayPath, overlay, 0o644); err != nil {
		return err
	}

	bin := filepath.Join(tmp, "check")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	build := exec.Command("go", "build", "-overlay="+overlayPath, "-o", bin, "./"+filepath.Base(tmp))
	build.Dir = pkgDir
	if out, err := build.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to build the program validating values decoded with UnmarshalText: %v\n%s", err, out)
	}

	var stdout, stderr bytes.Buffer
	cmd := exec.Command(bin)
	cmd.Dir = pkgDir
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &exit) && exit.ExitCode() == 3:
		return errors.New(strings.TrimSpace(stdout.String()))
	default:
		return fmt.Errorf("the program validating values decoded with UnmarshalText failed: %v\n%s%s",
			err, stdout.String(), stderr.String())
	}
}

// renderTextKeyChecks renders the statements of the check function comparing logical keys
// decoded with encoding.TextUnmarshaler by value: duplicate keys, and references to such keys.
// Values that fail to decode are skipped; they are reported as invalid.
func renderTextKeyChecks(b *bytes.Buffer, im *output.Imports, fmtPkg, decode string, datasets []*Dataset, enums *loader.Enums) {
	byID := map[string]*Dataset{}
	for _, ds := range datasets {
		byID[ds.Env+"\x00"+ds.Name] = ds
	}

	for _, ds := range datasets {
		if !hasTextKey(ds, enums) || len(ds.Records) == 0 {
			continue
		}
		var names, fields []string
		for i, k := range ds.Schema.Keys {
			names = append(names, k.Name())
			typ := "string" // other key fields are compared by their literal, as in checkDuplicateKeys
			if isText(k.Type(), enums) {
				typ = im.TypeString(k.Type())
			}
			fields = append(fields, fmt.Sprintf("k%d %s", i, typ))
		}
		fmt.Fprintf(b, `{
type datlasKey struct{ %s }
datlasSeen := map[datlasKey]string{}
datlasAdd := func(k datlasKey, pos, display string) {
	if prev, dup := datlasSeen[k]; dup {
		datlasErrs = append(datlasErrs, %s.Sprintf("%%s: duplicate logical key %%s = %%s (first defined at %%s)", pos, %s, display, prev))
		return
	}
	datlasSeen[k] = pos
}
`, strings.Join(fields, "; "), fmtPkg, strconv.Quote(strings.Join(names, ", ")))
		for _, r := range ds.Records {
			var decls, conds, vals, display []string
			for i, k := range ds.Schema.Keys {
				if isText(k.Type(), enums) {
					decls = append(decls, fmt.Sprintf("k%d, ok%d := %s[%s](%s)", i, i, decode, im.TypeString(k.Type()), strconv.Quote(r.KeyTexts[i])))
					conds = append(conds, fmt.Sprintf("ok%d", i))
					vals = append(vals, fmt.Sprintf("k%d", i))
					display = append(display, strconv.Quote(r.KeyTexts[i]))
				} else {
					vals = append(vals, strconv.Quote(r.Keys[i]))
					display = append(display, r.Keys[i])
				}
			}
			fmt.Fprintf(b, "{\n%s\nif %s {\ndatlasAdd(datlasKey{%s}, %s, %s)\n}\n}\n",
				strings.Join(decls, "\n"), strings.Join(conds, " && "), strings.Join(vals, ", "),
				strconv.Quote(r.Pos), strconv.Quote(strings.Join(display, ", ")))
		}
		b.WriteString("}\n")
	}

	// References to keys decoded with encoding.TextUnmarshaler, grouped by the referenced dataset.
	type refUse struct {
		ds *Dataset
		rv RefValue
	}
	var targets []string
	uses := map[string][]refUse{}
	for _, ds := range datasets {
		for _, r := range ds.Records {
			for _, rv := range r.Refs {
				if rv.Type == nil {
					continue
				}
				id := ds.Env + "\x00" + rv.Ref.Dataset
				if _, ok := uses[id]; !ok {
					targets = append(targets, id)
				}
				uses[id] = append(uses[id], refUse{ds, rv})
			}
		}
	}
	for _, id := range targets {
		target := byID[id]
		var keyType types.Type
		if target != nil && len(target.Schema.Keys) == 1 {
			keyType = target.Schema.Keys[0].Type()
		} else {
			keyType = uses[id][0].rv.Type // no such dataset in the environment: every reference fails
		}
		typ := im.TypeString(keyType)
		fmt.Fprintf(b, "{\ndatlasKeys := map[%s]bool{}\n", typ)
		if target != nil {
			for _, r := range target.Records {
				fmt.Fprintf(b, "if k, ok := %s[%s](%s); ok {\ndatlasKeys[k] = true\n}\n", decode, typ, strconv.Quote(r.KeyTexts[0]))
			}
		}
		for _, u := range uses[id] {
			where := fmt.Sprintf("dataset %q", u.rv.Ref.String())
			if u.ds.Env != "" {
				where += fmt.Sprintf(" of the %q environment", u.ds.Env)
			}
			msg := fmt.Sprintf("%s: %s refers to %s, which does not exist in %s", u.rv.Pos, u.rv.Ref.Field.Name(), u.rv.Lit, where)
			fmt.Fprintf(b, "if v, ok := %s[%s](%s); ok && !datlasKeys[v] {\ndatlasErrs = append(datlasErrs, %s)\n}\n",
				decode, typ, strconv.Quote(u.rv.Text), strconv.Quote(msg))
		}
		b.WriteString("}\n")
	}
}
