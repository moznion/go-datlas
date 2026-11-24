package dbapply_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/moznion/go-datlas/example/dbapply/edgecases"
	"github.com/moznion/go-datlas/example/masterdata"
	"github.com/moznion/go-datlas/example/seeddata"
)

func TestDiffPostgres(t *testing.T) {
	db := openDB(t, ddl)
	ctx := t.Context()
	// diff returns the changes DiffMasterData and DiffSeedData report but the unchanged rows, which it counts.
	diff := func() ([]string, int) {
		t.Helper()
		mc, err := masterdata.DiffMasterData(ctx, db)
		if err != nil {
			t.Fatal(err)
		}
		sc, err := seeddata.DiffSeedData(ctx, db, "local")
		if err != nil {
			t.Fatal(err)
		}
		var changes []string
		unchanged := 0
		for _, c := range mc {
			if c.Op == masterdata.MasterDataUnchanged {
				unchanged++
				continue
			}
			changes = append(changes, c.String())
		}
		for _, c := range sc {
			if c.Op == seeddata.SeedDataUnchanged {
				unchanged++
				continue
			}
			changes = append(changes, c.String())
		}
		return changes, unchanged
	}

	// An empty database: every record is inserted, and nothing is written.
	changes, unchanged := diff()
	for _, want := range []string{
		"insert roles (admin): roles.yaml #1",
		"insert permissions (admin, write): permissions.yaml #2",
		"insert users (bob@example.test): local/users.yaml #2",
	} {
		if !slices.Contains(changes, want) {
			t.Errorf("changes of an empty database lack %q:\n%s", want, strings.Join(changes, "\n"))
		}
	}
	for _, c := range changes {
		if !strings.HasPrefix(c, "insert ") {
			t.Errorf("change of an empty database: %s", c)
		}
	}
	if unchanged != 0 {
		t.Errorf("%d unchanged rows in an empty database", unchanged)
	}
	if got := query[string](t, db, `SELECT ((SELECT count(*) FROM roles) + (SELECT count(*) FROM users))::text`); got != "0" {
		t.Errorf("diff wrote %s rows", got)
	}

	apply(t, db, "local")
	if changes, unchanged := diff(); len(changes) != 0 || unchanged == 0 {
		t.Errorf("changes after applying (%d unchanged):\n%s", unchanged, strings.Join(changes, "\n"))
	}

	for _, stmt := range []string{
		`UPDATE roles SET name = 'edited' WHERE code = 'admin'`,
		`INSERT INTO roles VALUES ('guest', 'Guest')`,
		`INSERT INTO permissions VALUES ('guest', 'read')`,
		`INSERT INTO companies VALUES ('stale', 'Stale')`,
		`UPDATE users SET role = 'admin', joined_at = joined_at + interval '1 day' WHERE email = 'bob@example.test'`,
		`INSERT INTO users VALUES ('carol@example.test', 'acme', 'member', now())`,
	} {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	// Upserts come in the applying order, then prunes in the reverse order; users does not prune.
	want := []string{
		"update roles (admin): roles.yaml #1: name",
		"delete permissions (guest, read)",
		"delete roles (guest)",
		"update users (bob@example.test): local/users.yaml #2: role, joined_at",
		"delete companies (stale)",
	}
	if changes, _ := diff(); !slices.Equal(changes, want) {
		t.Errorf("changes:\n%s\nwant\n%s", strings.Join(changes, "\n"), strings.Join(want, "\n"))
	}

	// Applying them leaves nothing to change.
	apply(t, db, "local")
	if changes, _ := diff(); len(changes) != 0 {
		t.Errorf("changes after applying again:\n%s", strings.Join(changes, "\n"))
	}
	if _, err := seeddata.DiffSeedData(ctx, db, "production"); err == nil {
		t.Error("unknown environment accepted")
	}
}

func TestDiffPostgresEdgeCases(t *testing.T) {
	db := openDB(t, edgeDDL)
	ctx := t.Context()
	diff := func() []string {
		t.Helper()
		var changes []string
		mc, err := edgecases.DiffMasterData(ctx, db)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range mc {
			changes = append(changes, c.String())
		}
		sc, err := edgecases.DiffSeedData(ctx, db, "local")
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range sc {
			changes = append(changes, c.String())
		}
		return changes
	}
	const owner, viewer = "0191d3a0-0000-7000-8000-000000000001", "0191d3a0-0000-7000-8000-000000000002"
	want := []string{
		"insert grades (" + owner + "): grades.yaml #1",
		"insert grades (" + viewer + "): grades.yaml #2",
		// A table named excluded and a schema-qualified one are compared like the others.
		"insert excluded (a): leftovers.yaml #1",
		"insert notes (n1): notes.yaml #1",
		"insert config.settings (locale): settings.yaml #1",
		"insert config.settings (theme): settings.yaml #2",
		// An empty dataset deletes every row, of a schema-qualified table too.
		"delete config.flags (stale)",
		// Records without a key are always inserted.
		"insert events: local/events.yaml #1",
		"insert events: local/events.yaml #2",
		"delete tags (stale)",
	}
	if changes := diff(); !slices.Equal(changes, want) {
		t.Errorf("changes:\n%s\nwant\n%s", strings.Join(changes, "\n"), strings.Join(want, "\n"))
	}

	if err := edgecases.ApplyMasterData(ctx, db); err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		// The label is insert-only, so renaming it changes nothing applying does.
		`UPDATE grades SET label = 'Edited' WHERE id = '` + owner + `'`,
		`UPDATE grades SET permission = '{"actions": []}' WHERE id = '` + viewer + `'`,
		// Rows of the application are left alone, since grades does not prune.
		`INSERT INTO grades (label, company_id, permission) VALUES ('Custom', 'acme', '{}')`,
		// The json column is compared as jsonb: the order of the keys does not matter.
		`UPDATE notes SET body = '{"b": "x", "a": "y"}'`,
	} {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	mc, err := edgecases.DiffMasterData(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range mc {
		got = append(got, c.String())
	}
	want = []string{
		"unchanged grades (" + owner + "): grades.yaml #1",
		"update grades (" + viewer + "): grades.yaml #2: permission",
		"unchanged excluded (a): leftovers.yaml #1",
		// The score and the time are compared once rounded as the columns store them.
		"unchanged notes (n1): notes.yaml #1",
		"unchanged config.settings (locale): settings.yaml #1",
		"unchanged config.settings (theme): settings.yaml #2",
	}
	if !slices.Equal(got, want) {
		t.Errorf("changes:\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if c := mc[1]; c.Op != edgecases.MasterDataUpdate || c.Table != "grades" || c.Dataset != "grades" || c.Record != 2 ||
		!slices.Equal(c.Key, []string{viewer}) || !slices.Equal(c.Columns, []string{"permission"}) {
		t.Errorf("change: %+v", c)
	}

	if _, err := db.ExecContext(ctx, `UPDATE notes SET score = 1.2, body = '{"a": "z"}'`); err != nil {
		t.Fatal(err)
	}
	mc, err = edgecases.DiffMasterData(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if i := slices.IndexFunc(mc, func(c edgecases.MasterDataChange) bool { return c.Table == "notes" }); i < 0 {
		t.Error("no change of notes")
	} else if got, want := mc[i].String(), "update notes (n1): notes.yaml #1: score, body"; got != want {
		t.Errorf("change = %q, want %q", got, want)
	}
}
