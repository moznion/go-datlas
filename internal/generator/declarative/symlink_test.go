package declarative

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/moznion/go-datlas/internal/generator/testutil"
)

func symlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symbolic links are not supported: %v", err)
	}
}

func TestSeedSymlinks(t *testing.T) {
	dir := testutil.WriteTree(t, map[string]string{
		"schema.go":           userSeedSchema,
		"local/users.yaml":    "- email: a@example.test\n",
		"_common/events.yaml": "- title: Launch\n",
	})
	symlink(t, "local", filepath.Join(dir, "e2e"))                                   // an environment
	symlink(t, "../_common/events.yaml", filepath.Join(dir, "local", "events.yaml")) // a data file
	path, err := run(SeedData, Config{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	testutil.TypeCheck(t, dir, ".")
	mustContain(t, testutil.ReadFile(t, path),
		`func SeedEnvironments() []string { return []string{"e2e", "local"} }`,
		`func E2eUserByEmail(key string) (UserSeed, bool) { switch key { case "a@example.test":`,
		`func LocalEvents() []EventSeed { return []EventSeed{ datlasSeedLocalEvents0(), } }`,
		`func E2eEvents() []EventSeed { return []EventSeed{ datlasSeedE2eEvents0(), } }`,
	)
}

func TestBrokenSymlinks(t *testing.T) {
	t.Run("environment", func(t *testing.T) {
		dir := testutil.WriteTree(t, map[string]string{"schema.go": userSeedSchema, "local/users.yaml": "[]\n"})
		symlink(t, "missing", filepath.Join(dir, "e2e"))
		_, err := run(SeedData, Config{Dir: dir})
		mustFail(t, err, "e2e: broken symbolic link")
	})
	t.Run("data file", func(t *testing.T) {
		dir := testutil.WriteTree(t, map[string]string{"schema.go": roleSchema})
		symlink(t, "missing.yaml", filepath.Join(dir, "roles.yaml"))
		_, err := run(MasterData, Config{Dir: dir})
		mustFail(t, err, "roles.yaml: broken symbolic link")
	})
}
