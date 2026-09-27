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

	"golang.org/x/tools/go/packages"

	"github.com/moznion/go-datlas/internal/generator/output"
)

// runTextChecks validates the values of types decoded with encoding.TextUnmarshaler.
//
// The generator cannot run the user's UnmarshalText methods itself. Instead it adds, through a
// build overlay, a function to the schema package that decodes every value (so unexported types
// work too), and builds and runs a temporary program calling it. The overlay also replaces the
// generated file by its new content, so a stale or missing generated file does not matter.
// Nothing is written to the package except a temporary directory, removed afterwards.
func runTextChecks(pkg *packages.Package, pkgDir, genPath string, genSrc []byte, checks []textCheck) error {
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
	for i := 2; scope.Lookup(checkFunc) != nil; i++ {
		checkFunc = "DatlasTextCheck" + strconv.Itoa(i)
	}

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
	body.WriteString("return datlasErrs\n}\n")

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
