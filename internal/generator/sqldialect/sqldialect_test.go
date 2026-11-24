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
			`INSERT INTO "roles" AS "datlas_target" ("code", "name") VALUES ($1, $2) ON CONFLICT ("code") DO UPDATE SET "name" = EXCLUDED."name"`},
		{d.Upsert(UpsertSpec{Table: "perms", Cols: []string{"role", "action"}, Keys: []string{"role", "action"}}),
			`INSERT INTO "perms" AS "datlas_target" ("role", "action") VALUES ($1, $2) ON CONFLICT ("role", "action") DO NOTHING`},
		// A table named excluded does not clash with EXCLUDED, thanks to the alias.
		{d.Upsert(UpsertSpec{Table: "excluded", Cols: []string{"k", "v"}, Keys: []string{"k"}}),
			`INSERT INTO "excluded" AS "datlas_target" ("k", "v") VALUES ($1, $2) ON CONFLICT ("k") DO UPDATE SET "v" = EXCLUDED."v"`},
		{d.Upsert(UpsertSpec{Table: "roles", Cols: []string{"id", "label", "perm"}, Keys: []string{"id"}, InsertOnly: []string{"label"}}),
			`INSERT INTO "roles" AS "datlas_target" ("id", "label", "perm") VALUES ($1, $2, $3) ON CONFLICT ("id") DO UPDATE SET "perm" = EXCLUDED."perm"`},
		// Every column but the key is insert-only: nothing to update, so the touch column is left alone too.
		{d.Upsert(UpsertSpec{Table: "roles", Cols: []string{"id", "label"}, Keys: []string{"id"}, InsertOnly: []string{"label"}, Touch: "updated_at"}),
			`INSERT INTO "roles" AS "datlas_target" ("id", "label") VALUES ($1, $2) ON CONFLICT ("id") DO NOTHING`},
		{d.Upsert(UpsertSpec{Table: "roles", Cols: []string{"id", "perm"}, Keys: []string{"id"}, Touch: "updated_at"}),
			`INSERT INTO "roles" AS "datlas_target" ("id", "perm") VALUES ($1, $2) ON CONFLICT ("id") DO UPDATE SET "perm" = EXCLUDED."perm", "updated_at" = CURRENT_TIMESTAMP`},
		{d.DeleteExcept("roles", []string{"code"}, 0), `DELETE FROM "roles"`},
		{d.DeleteExcept("roles", []string{"code"}, 2), `DELETE FROM "roles" WHERE "code" NOT IN ($1, $2)`},
		{d.DeleteExcept("perms", []string{"role", "action"}, 2),
			`DELETE FROM "perms" WHERE ("role", "action") NOT IN (($1, $2), ($3, $4))`},
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
