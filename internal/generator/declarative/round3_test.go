package declarative

import (
	"testing"
)

func TestUnrelatedNestedYAMLIsIgnored(t *testing.T) {
	_, err := generate(t, MasterData, map[string]string{
		"schema.go":           roleSchema,
		"roles.yaml":          "[]\n",
		"deploy/k8s/app.yaml": "a: 1\n",
		"api/openapi.yaml":    "openapi: 3.0.0\n",
		"a/b/c/roles.yaml":    "[]\n", // deeper than the misplaced-file search
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = generate(t, MasterData, map[string]string{"schema.go": roleSchema, "roles.yaml": "[]\n", "old/roles.yaml": "[]\n"})
	mustFail(t, err, "old/roles.yaml: dataset files in subdirectories are not loaded; move it to ")
}

func TestIntegerEnumMessage(t *testing.T) {
	schema := "package data\n\n//datlas:enum\ntype Tier int\n\nconst (\n\tTierFree Tier = iota + 1\n\tTierPro\n)\n\n//datlas:master plans\ntype Plan struct {\n\tCode string `master:\"key\"`\n\tTier Tier\n}\n"
	_, err := generate(t, MasterData, map[string]string{"schema.go": schema, "plans.yaml": "- {code: a, tier: pro}\n"})
	mustFail(t, err, "integer enums are written with their numeric value: TierFree=1, TierPro=2")
}
