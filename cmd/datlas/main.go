// Command datlas generates typed Go code for fixtures, master data and seed data.
//
// Usage:
//
//	datlas fixture [-out DIR] [-pkg NAME] [-config FILE] [-check] [PACKAGES...]
//	datlas master  [-dir DIR] [-check] [PACKAGE]
//	datlas seed    [-dir DIR] [-check] [PACKAGE]
//	datlas version
//
// It is typically run through go:generate.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime/debug"

	"github.com/moznion/go-datlas/internal/generator/declarative"
	"github.com/moznion/go-datlas/internal/generator/fixture"
	"github.com/moznion/go-datlas/internal/generator/output"
)

const usage = `datlas generates typed Go code for fixtures, master data and seed data.

Usage:

	datlas fixture [-out DIR] [-pkg NAME] [-config FILE] [-check] [PACKAGES...]
	datlas master  [-dir DIR] [-check] [PACKAGE]
	datlas seed    [-dir DIR] [-check] [PACKAGE]
	datlas version

Run "datlas <command> -h" for the details of a command.
`

var commandUsages = map[string]string{
	"fixture": `Usage: datlas fixture [-out DIR] [-pkg NAME] [-config FILE] [-check] [PACKAGES...]

Generate a fixture factory (zz_fixture_gen.go) into the package in DIR
for the //datlas:fixture types found in PACKAGES and the types listed in
the policy file given with -config.

Without PACKAGES and -config, only the factory itself is generated,
so that factory methods can be added to it by hand.

`,
	"master": `Usage: datlas master [-dir DIR] [-check] [PACKAGE]

Compile the master data YAML files in DIR (<name>.yaml) into
zz_masterdata_gen.go of PACKAGE, using its //datlas:master schemas.
PACKAGE defaults to the package in the current directory.

`,
	"seed": `Usage: datlas seed [-dir DIR] [-check] [PACKAGE]

Compile the seed data YAML files in DIR (<env>/<name>.yaml) into
zz_seeddata_gen.go of PACKAGE, using its //datlas:seed schemas.
PACKAGE defaults to the package in the current directory.

`,
}

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		if !errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(os.Stderr, "datlas:", err)
		}
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return errors.New("missing command")
	}
	wd, err := os.Getwd()
	if err != nil {
		return err
	}

	cmd, args := args[0], args[1:]
	switch cmd {
	case "help", "-h", "-help", "--help":
		fmt.Fprint(stdout, usage)
		return nil
	case "version", "-version", "--version":
		fmt.Fprintln(stdout, "datlas", version())
		return nil
	}
	cmdUsage, ok := commandUsages[cmd]
	if !ok {
		fmt.Fprint(stderr, usage)
		return fmt.Errorf("unknown command %q", cmd)
	}

	fs := flag.NewFlagSet("datlas "+cmd, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprint(stderr, cmdUsage)
		fmt.Fprintln(stderr, "Flags:")
		fs.PrintDefaults()
	}
	check := fs.Bool("check", false, "do not write anything; fail if the generated file is not up to date")

	var result output.Result
	switch cmd {
	case "fixture":
		out := fs.String("out", ".", "directory of the package the factory is generated into")
		pkg := fs.String("pkg", "", "name of the generated package (default: derived from -out)")
		config := fs.String("config", "", "YAML file giving the fixture policy of types that cannot be annotated")
		if err := fs.Parse(args); err != nil {
			return err
		}
		result, err = fixture.Generate(fixture.Config{Dir: wd, Patterns: fs.Args(), PolicyFile: *config, OutDir: *out, PackageName: *pkg})

	case "master", "seed":
		dir := fs.String("dir", ".", "directory holding the YAML files, relative to the package directory")
		if err := fs.Parse(args); err != nil {
			return err
		}
		if fs.NArg() > 1 {
			fs.Usage()
			return fmt.Errorf("%s: at most one package can be given", cmd)
		}
		kind := declarative.MasterData
		if cmd == "seed" {
			kind = declarative.SeedData
		}
		result, err = declarative.Generate(kind, declarative.Config{Dir: wd, Package: fs.Arg(0), DataDir: *dir})
	}
	if err != nil {
		return err
	}
	msg, err := output.Apply(result, *check)
	if err != nil {
		return err
	}
	if msg != "" {
		fmt.Fprintln(stderr, "datlas:", msg)
	}
	return nil
}

// version returns the module version datlas was built from.
func version() string {
	const modulePath = "github.com/moznion/go-datlas"
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "(unknown)"
	}
	// Built with `go install` or `go run pkg@version`, datlas is the main module;
	// run with `go tool`, it is a dependency of the main module.
	mods := append([]*debug.Module{&info.Main}, info.Deps...)
	for _, m := range mods {
		if m.Path == modulePath && m.Version != "" {
			if m.Replace != nil {
				return m.Version + " (replaced by " + m.Replace.Path + ")"
			}
			return m.Version
		}
	}
	return "(unknown)"
}
