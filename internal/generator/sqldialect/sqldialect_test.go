package sqldialect

import "testing"

func TestPostgres(t *testing.T) {
	d, err := Lookup("postgres")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct{ got, want string }{
		{d.Insert("app.users", []string{"email", "name"}), `INSERT INTO "app"."users" ("email", "name") VALUES ($1, $2)`},
		{d.Upsert(UpsertSpec{Table: "roles", Cols: []string{"code", "name"}, Keys: []string{"code"}}),
			`INSERT INTO "roles" ("code", "name") VALUES ($1, $2) ON CONFLICT ("code") DO UPDATE SET "name" = EXCLUDED."name"`},
		{d.Upsert(UpsertSpec{Table: "perms", Cols: []string{"role", "action"}, Keys: []string{"role", "action"}}),
			`INSERT INTO "perms" ("role", "action") VALUES ($1, $2) ON CONFLICT ("role", "action") DO NOTHING`},
		{d.Upsert(UpsertSpec{Table: "roles", Cols: []string{"id", "label", "perm"}, Keys: []string{"id"}, InsertOnly: []string{"label"}}),
			`INSERT INTO "roles" ("id", "label", "perm") VALUES ($1, $2, $3) ON CONFLICT ("id") DO UPDATE SET "perm" = EXCLUDED."perm"`},
		// Every column but the key is insert-only: nothing to update, so the touch column is left alone too.
		{d.Upsert(UpsertSpec{Table: "roles", Cols: []string{"id", "label"}, Keys: []string{"id"}, InsertOnly: []string{"label"}, Touch: "updated_at", SkipUnchanged: true}),
			`INSERT INTO "roles" ("id", "label") VALUES ($1, $2) ON CONFLICT ("id") DO NOTHING`},
		{d.Upsert(UpsertSpec{Table: "roles", Cols: []string{"id", "perm"}, Keys: []string{"id"}, Touch: "updated_at"}),
			`INSERT INTO "roles" ("id", "perm") VALUES ($1, $2) ON CONFLICT ("id") DO UPDATE SET "perm" = EXCLUDED."perm", "updated_at" = CURRENT_TIMESTAMP`},
		{d.Upsert(UpsertSpec{Table: "app.roles", Cols: []string{"id", "perm"}, Keys: []string{"id"}, Touch: "updated_at", SkipUnchanged: true}),
			`INSERT INTO "app"."roles" ("id", "perm") VALUES ($1, $2) ON CONFLICT ("id") DO UPDATE SET "perm" = EXCLUDED."perm", "updated_at" = CURRENT_TIMESTAMP WHERE "app"."roles"."perm" IS DISTINCT FROM EXCLUDED."perm"`},
		{d.Upsert(UpsertSpec{Table: "roles", Cols: []string{"id", "name", "perm"}, Keys: []string{"id"}, SkipUnchanged: true}),
			`INSERT INTO "roles" ("id", "name", "perm") VALUES ($1, $2, $3) ON CONFLICT ("id") DO UPDATE SET "name" = EXCLUDED."name", "perm" = EXCLUDED."perm" WHERE ("roles"."name", "roles"."perm") IS DISTINCT FROM (EXCLUDED."name", EXCLUDED."perm")`},
		{d.DeleteExcept("roles", []string{"code"}, 0), `DELETE FROM "roles"`},
		{d.DeleteExcept("roles", []string{"code"}, 2), `DELETE FROM "roles" WHERE "code" NOT IN ($1, $2)`},
		{d.DeleteExcept("perms", []string{"role", "action"}, 2),
			`DELETE FROM "perms" WHERE ("role", "action") NOT IN (($1, $2), ($3, $4))`},
		{d.SelectExcept("app.roles", []string{"code"}, 0), `SELECT CAST("code" AS text) FROM "app"."roles" ORDER BY "code"`},
		{d.SelectExcept("perms", []string{"role", "action"}, 2),
			`SELECT CAST("role" AS text), CAST("action" AS text) FROM "perms" WHERE ("role", "action") NOT IN (($1, $2), ($3, $4)) ORDER BY "role", "action"`},
		{d.Compare("roles", []string{"code"}, []string{"name", "perm"}),
			`SELECT "name" IS DISTINCT FROM $2, "perm" IS DISTINCT FROM $3 FROM "roles" WHERE "code" = $1`},
		{d.Compare("app.perms", []string{"role", "action"}, nil), `SELECT 1 FROM "app"."perms" WHERE "role" = $1 AND "action" = $2`},
	}
	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("got  %s\nwant %s", tt.got, tt.want)
		}
	}
}

func TestLookupUnknown(t *testing.T) {
	if _, err := Lookup("oracle"); err == nil || err.Error() != `unknown SQL dialect "oracle" (available: postgres)` {
		t.Fatalf("err = %v", err)
	}
}

func TestValidTable(t *testing.T) {
	for name, want := range map[string]bool{"users": true, "app.users": true, "_t1": true, "1t": false, "a.b.c": false, "a-b": false, "": false, "a.": false} {
		if got := ValidTable(name); got != want {
			t.Errorf("ValidTable(%q) = %v", name, got)
		}
	}
}
