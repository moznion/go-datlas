package sqldialect

import "testing"

func TestPostgres(t *testing.T) {
	d, err := Lookup("postgres")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct{ got, want string }{
		{d.Insert("app.users", []string{"email", "name"}), `INSERT INTO "app"."users" ("email", "name") VALUES ($1, $2)`},
		{d.Upsert("roles", []string{"code", "name"}, []string{"code"}),
			`INSERT INTO "roles" ("code", "name") VALUES ($1, $2) ON CONFLICT ("code") DO UPDATE SET "name" = EXCLUDED."name"`},
		{d.Upsert("perms", []string{"role", "action"}, []string{"role", "action"}),
			`INSERT INTO "perms" ("role", "action") VALUES ($1, $2) ON CONFLICT ("role", "action") DO NOTHING`},
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
