package declarative

import (
	"reflect"
	"strings"
	"testing"

	"github.com/moznion/go-datlas/internal/generator/testutil"
)

func TestMasterKeyConsts(t *testing.T) {
	dir := testutil.WriteTree(t, map[string]string{
		"schema.go": "package data\n\n" +
			"type Code string\n\n" +
			"//datlas:master roles\ntype Role struct {\n\tCode Code `yaml:\"code\" master:\"key\"`\n}\n\n" +
			"//datlas:master levels\ntype Level struct {\n\tID int `yaml:\"id\" master:\"key\"`\n}\n\n" +
			"//datlas:master people singular=Person\ntype Person struct {\n\tName string `yaml:\"name\" master:\"key\"`\n}\n\n" +
			"//datlas:master flags consts=false\ntype Flag struct {\n\tName string `yaml:\"name\" master:\"key\"`\n}\n\n" +
			"//datlas:master pairs\ntype Pair struct {\n\tA string `yaml:\"a\" master:\"key\"`\n\tB string `yaml:\"b\" master:\"key\"`\n}\n\n" +
			"//datlas:enum\ntype Tier string\n\nconst TierFree Tier = \"free\"\n\n" +
			"//datlas:master tiers\ntype TierDef struct {\n\tTier Tier `yaml:\"tier\" master:\"key\"`\n}\n",
		"roles.yaml":  "- code: admin\n- code: read-only\n- code: \"v2 user\"\n",
		"levels.yaml": "- id: 1\n- id: -3\n",
		"people.yaml": "- name: alice\n",
		"flags.yaml":  "- name: beta\n",
		"pairs.yaml":  "- a: x\n  b: y\n",
		"tiers.yaml":  "- tier: free\n",
	})
	path, err := run(MasterData, Config{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	testutil.TypeCheck(t, dir, ".")
	src := testutil.ReadFile(t, path)
	mustContain(t, src,
		`RoleCodeAdmin Code = "admin"`,
		`RoleCodeReadOnly Code = "read-only"`,
		`RoleCodeV2User Code = "v2 user"`,
		`LevelID1 int = 1`,
		`LevelIDMinus3 int = -3`,
		`PersonNameAlice string = "alice"`,
	)
	for _, unwanted := range []string{"FlagName", "PairA", "TierDefTier", "TierTier"} {
		if strings.Contains(src, unwanted) {
			t.Errorf("generated code contains %s:\n%s", unwanted, src)
		}
	}
	// The lookup switch keeps literals: seed data of other packages reads the keys from it.
	mustContain(t, src, `case "admin": return datlasMasterRoles0(), true`)
}

func TestMasterKeyConstConflicts(t *testing.T) {
	schema := "package data\n\n//datlas:master roles\ntype Role struct {\n\tCode string `yaml:\"code\" master:\"key\"`\n}\n"
	t.Run("between keys", func(t *testing.T) {
		_, err := generate(t, MasterData, map[string]string{
			"schema.go":  schema,
			"roles.yaml": "- code: read-only\n- code: read_only\n",
		})
		mustFail(t, err, "generated identifier RoleCodeReadOnly conflicts with")
		mustFail(t, err, "consts=false")
	})
	t.Run("with a declaration", func(t *testing.T) {
		_, err := generate(t, MasterData, map[string]string{
			"schema.go":  schema + "\nconst RoleCodeAdmin = \"admin\"\n",
			"roles.yaml": "- code: admin\n",
		})
		mustFail(t, err, "generated identifier RoleCodeAdmin conflicts with the declaration at")
	})
	t.Run("disabled", func(t *testing.T) {
		_, err := generate(t, MasterData, map[string]string{
			"schema.go":  strings.Replace(schema, "master roles", "master roles consts=false", 1) + "\nconst RoleCodeAdmin = \"admin\"\n",
			"roles.yaml": "- code: admin\n",
		})
		if err != nil {
			t.Fatal(err)
		}
	})
	t.Run("seed data has no consts option", func(t *testing.T) {
		_, err := generate(t, SeedData, map[string]string{
			"schema.go":        "package data\n\n//datlas:seed users consts=false\ntype U struct {\n\tA string `seed:\"key\"`\n}\n",
			"local/users.yaml": "[]\n",
		})
		mustFail(t, err, `unknown or repeated option "consts=false" (available: singular=<Name>)`)
	})
}

func TestDatasetOrder(t *testing.T) {
	ds := func(name string, refs ...string) *Dataset {
		d := &Dataset{Name: name}
		for _, r := range refs {
			pkg, dataset, ok := strings.Cut(r, ":")
			if !ok {
				pkg, dataset = "", r
			}
			d.Schema.Refs = append(d.Schema.Refs, Ref{Package: pkg, Dataset: dataset})
		}
		return d
	}
	tests := []struct {
		name     string
		datasets []*Dataset
		want     []string
	}{
		{"no refs", []*Dataset{ds("b"), ds("a")}, []string{"a", "b"}},
		{"chain", []*Dataset{ds("a", "b"), ds("b", "c"), ds("c")}, []string{"c", "b", "a"}},
		{"self and external refs are ignored", []*Dataset{ds("a", "a", "x/y:z"), ds("b", "a")}, []string{"a", "b"}},
		{"diamond", []*Dataset{ds("d", "b", "c"), ds("b", "a"), ds("c", "a"), ds("a")}, []string{"a", "b", "c", "d"}},
		{"cycle", []*Dataset{ds("a", "c"), ds("c", "d"), ds("d", "c"), ds("b")}, []string{"b", "c", "d", "a"}},
		{"duplicate environments", []*Dataset{ds("users", "companies"), ds("companies"), ds("users", "companies")}, []string{"companies", "users"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := datasetOrder(tt.datasets); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDatasetOrderGenerated(t *testing.T) {
	t.Run("master", func(t *testing.T) {
		path, err := generate(t, MasterData, map[string]string{
			"schema.go": "package data\n\n" +
				"//datlas:master roles\ntype Role struct {\n\tCode string `yaml:\"code\" master:\"key\"`\n}\n\n" +
				"//datlas:master permissions\ntype Permission struct {\n\tRole string `yaml:\"role\" master:\"key,ref=roles\"`\n}\n",
			"roles.yaml":       "- code: admin\n",
			"permissions.yaml": "- role: admin\n",
		})
		if err != nil {
			t.Fatal(err)
		}
		mustContain(t, testutil.ReadFile(t, path), `func MasterDatasets() []string { return []string{"roles", "permissions"} }`)
	})
	t.Run("seed", func(t *testing.T) {
		dir := testutil.WriteTree(t, map[string]string{
			"schema.go": "package data\n\n" +
				"//datlas:seed users\ntype User struct {\n\tCompany string `yaml:\"company\" seed:\"ref=zones\"`\n}\n\n" +
				"//datlas:seed zones\ntype Zone struct {\n\tKey string `yaml:\"key\" seed:\"key\"`\n}\n",
			"local/zones.yaml": "- key: z\n",
			"local/users.yaml": "- company: z\n",
		})
		path, err := run(SeedData, Config{Dir: dir})
		if err != nil {
			t.Fatal(err)
		}
		testutil.TypeCheck(t, dir, ".")
		mustContain(t, testutil.ReadFile(t, path),
			`func SeedDatasets() []string { return []string{"zones", "users"} }`,
			"type SeedSet struct { // Zones is the seed data \"zones\". Zones []Zone // Users is the seed data \"users\". Users []User }",
			`return SeedSet{ Zones: LocalZones(), Users: LocalUsers(), }, true`,
		)
	})
	t.Run("reserved name", func(t *testing.T) {
		_, err := generate(t, MasterData, map[string]string{
			"schema.go":  roleSchema + "\nfunc MasterDatasets() {}\n",
			"roles.yaml": "[]\n",
		})
		mustFail(t, err, "generated identifier MasterDatasets conflicts with the declaration at")
	})
}
