package dbapply_test

import (
	"context"
	"database/sql"
	"os"
	"slices"
	"strings"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/moznion/go-datlas/example/dbapply/edgecases"
	"github.com/moznion/go-datlas/example/masterdata"
	"github.com/moznion/go-datlas/example/seeddata"
)

// recorder records the statements instead of executing them.
type recorder struct{ stmts []string }

func (r *recorder) ExecContext(_ context.Context, query string, _ ...any) (sql.Result, error) {
	r.stmts = append(r.stmts, query)
	return nil, nil
}

func TestApplyOrder(t *testing.T) {
	var r recorder
	if err := masterdata.ApplyMasterData(t.Context(), &r); err != nil {
		t.Fatal(err)
	}
	var tables []string
	for _, s := range r.stmts {
		verb, table := strings.Fields(s)[0], strings.Fields(s)[2]
		if t := verb + " " + table; !slices.Contains(tables, t) {
			tables = append(tables, t)
		}
	}
	// Upserts follow MasterDatasets; prunes run in the reverse order.
	want := []string{
		`INSERT "regions"`, `INSERT "plans"`, `INSERT "roles"`, `INSERT "permissions"`,
		`DELETE "permissions"`, `DELETE "roles"`, `DELETE "plans"`, `DELETE "regions"`,
	}
	if !slices.Equal(tables, want) {
		t.Fatalf("statements:\n%v\nwant\n%v", tables, want)
	}
	if err := seeddata.ApplySeedData(t.Context(), &r, "production"); err == nil {
		t.Fatal("unknown environment accepted")
	}
}

const ddl = `
DROP TABLE IF EXISTS users, companies, permissions, roles, plans, regions;
CREATE TABLE regions (id uuid PRIMARY KEY, code text NOT NULL UNIQUE, country text NOT NULL, name text NOT NULL);
CREATE TABLE plans (
	code text PRIMARY KEY, name text NOT NULL, tier text NOT NULL, price_yen integer NOT NULL, trial bigint NOT NULL,
	features jsonb NOT NULL, regions jsonb NOT NULL, description text, limits jsonb NOT NULL
);
CREATE TABLE roles (code text PRIMARY KEY, name text NOT NULL);
CREATE TABLE permissions (role text REFERENCES roles (code), action text, PRIMARY KEY (role, action));
CREATE TABLE companies (key text PRIMARY KEY, name text NOT NULL);
CREATE TABLE users (
	email text PRIMARY KEY, company text NOT NULL REFERENCES companies (key),
	role text NOT NULL REFERENCES roles (code), joined_at timestamptz NOT NULL
);
`

func openDB(t *testing.T, ddl string) *sql.DB {
	t.Helper()
	dsn := os.Getenv("DATLAS_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("DATLAS_TEST_POSTGRES_DSN is not set")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.ExecContext(t.Context(), ddl); err != nil {
		t.Fatal(err)
	}
	return db
}

func apply(t *testing.T, db *sql.DB, env string) {
	t.Helper()
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := masterdata.ApplyMasterData(t.Context(), tx); err != nil {
		t.Fatal(err)
	}
	if err := seeddata.ApplySeedData(t.Context(), tx, env); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func query[T any](t *testing.T, db *sql.DB, q string) T {
	t.Helper()
	var v T
	if err := db.QueryRowContext(t.Context(), q).Scan(&v); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return v
}

func TestApplyPostgres(t *testing.T) {
	db := openDB(t, ddl)
	apply(t, db, "local")

	checks := map[string]string{
		`SELECT count(*)::text FROM roles`:                                                                            "2",
		`SELECT count(*)::text FROM permissions`:                                                                      "3",
		`SELECT count(*)::text FROM users`:                                                                            "2",
		`SELECT trial::text FROM plans WHERE code = 'pro'`:                                                            "2592000000000000",
		`SELECT features::text FROM plans WHERE code = 'pro'`:                                                         `["basic", "sso"]`,
		`SELECT regions->>1 FROM plans WHERE code = 'pro'`:                                                            "0191d3a0-7b1c-7c3e-9a4f-2b6d8e0f1a25",
		`SELECT coalesce(description, 'NULL') FROM plans WHERE code = 'free'`:                                         "NULL",
		`SELECT id::text FROM regions WHERE code = 'virginia'`:                                                        "0191d3a0-7b1c-7c3e-9a4f-2b6d8e0f1a25",
		`SELECT to_char(joined_at AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24') FROM users WHERE email = 'bob@example.test'`: "2024-04-01 00",
	}
	for q, want := range checks {
		if got := query[string](t, db, q); got != want {
			t.Errorf("%s = %q, want %q", q, got, want)
		}
	}

	// Applying again updates rows in place, restores edited ones and deletes rows missing from the YAML:
	// always for master data, and for seed data with prune=true (companies, not users).
	for _, stmt := range []string{
		`UPDATE roles SET name = 'edited' WHERE code = 'admin'`,
		`INSERT INTO roles VALUES ('guest', 'Guest')`,
		`INSERT INTO permissions VALUES ('guest', 'read')`,
		`INSERT INTO companies VALUES ('stale', 'Stale')`,
		`INSERT INTO users VALUES ('carol@example.test', 'acme', 'member', now())`,
	} {
		if _, err := db.ExecContext(t.Context(), stmt); err != nil {
			t.Fatal(err)
		}
	}
	apply(t, db, "local")
	for q, want := range map[string]string{
		`SELECT name FROM roles WHERE code = 'admin'`:                                          "Administrator",
		`SELECT string_agg(code, ',' ORDER BY code) FROM roles`:                                "admin,member",
		`SELECT string_agg(role || ':' || action, ',' ORDER BY role, action) FROM permissions`: "admin:read,admin:write,member:read",
		`SELECT string_agg(key, ',' ORDER BY key) FROM companies`:                              "acme",
		`SELECT count(*)::text FROM users`:                                                     "3",
	} {
		if got := query[string](t, db, q); got != want {
			t.Errorf("%s = %q, want %q", q, got, want)
		}
	}
}

const edgeDDL = `
DROP SCHEMA IF EXISTS config CASCADE;
DROP TABLE IF EXISTS events, tags, grades, excluded;
CREATE SCHEMA config;
CREATE TABLE config.flags (name text PRIMARY KEY);
CREATE TABLE excluded (name text PRIMARY KEY, value text NOT NULL);
CREATE TABLE grades (
	id uuid PRIMARY KEY DEFAULT gen_random_uuid(), label text NOT NULL, company_id text, permission jsonb NOT NULL,
	updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE events (kind text NOT NULL, payload jsonb NOT NULL);
CREATE TABLE tags (name text PRIMARY KEY);
INSERT INTO config.flags VALUES ('stale');
INSERT INTO tags VALUES ('stale');
`

func TestApplyPostgresEdgeCases(t *testing.T) {
	db := openDB(t, edgeDDL)
	for range 2 {
		if err := edgecases.ApplyMasterData(t.Context(), db); err != nil {
			t.Fatal(err)
		}
		if err := edgecases.ApplySeedData(t.Context(), db, "local"); err != nil {
			t.Fatal(err)
		}
	}
	for q, want := range map[string]string{
		// An empty dataset deletes every row, here of a schema-qualified table.
		`SELECT count(*)::text FROM config.flags`: "0",
		// So does an empty seed data file of a dataset with prune=true.
		`SELECT count(*)::text FROM tags`: "0",
		// Records without a key are inserted on every application.
		`SELECT count(*)::text FROM events`:                              "4",
		`SELECT payload::text FROM events WHERE kind = 'signup' LIMIT 1`: `{"plan": "free"}`,
		`SELECT payload::text FROM events WHERE kind = 'login' LIMIT 1`:  `{}`,
	} {
		if got := query[string](t, db, q); got != want {
			t.Errorf("%s = %q, want %q", q, got, want)
		}
	}
}

// TestApplyPostgresUpsertOptions applies grades, whose label is insert-only and whose updated_at moves
// when a row is updated, to a table also holding rows of the application.
func TestApplyPostgresUpsertOptions(t *testing.T) {
	db := openDB(t, edgeDDL)
	exec := func(stmts ...string) {
		t.Helper()
		for _, stmt := range stmts {
			if _, err := db.ExecContext(t.Context(), stmt); err != nil {
				t.Fatalf("%s: %v", stmt, err)
			}
		}
	}
	applyMaster := func() {
		t.Helper()
		if err := edgecases.ApplyMasterData(t.Context(), db); err != nil {
			t.Fatal(err)
		}
	}
	const owner, viewer = `'0191d3a0-0000-7000-8000-000000000001'`, `'0191d3a0-0000-7000-8000-000000000002'`

	applyMaster()
	exec(
		// The application creates a row of its own and renames a label.
		`INSERT INTO grades (label, company_id, permission) VALUES ('Custom', 'acme', '{}')`,
		`UPDATE grades SET label = 'Edited' WHERE id = `+owner,
		`UPDATE grades SET permission = '{"actions": []}' WHERE id = `+viewer,
		`UPDATE grades SET updated_at = '2000-01-01T00:00:00Z'`,
	)
	applyMaster()

	for q, want := range map[string]string{
		// The label is written on insert only, so the renamed one stays.
		`SELECT string_agg(label, ',' ORDER BY label) FROM grades`: "Custom,Edited,Viewer",
		// The changed permission is restored, and every row of the master data is touched.
		`SELECT permission::text FROM grades WHERE id = ` + viewer:                            `{"actions": ["read"]}`,
		`SELECT (updated_at > '2000-01-01T00:00:00Z')::text FROM grades WHERE id = ` + viewer: "true",
		`SELECT (updated_at > '2000-01-01T00:00:00Z')::text FROM grades WHERE id = ` + owner:  "true",
		// prune=false leaves the rows of the application, untouched.
		`SELECT count(*)::text FROM grades WHERE company_id = 'acme'`:                                      "1",
		`SELECT bool_and(updated_at = '2000-01-01T00:00:00Z')::text FROM grades WHERE company_id = 'acme'`: "true",
	} {
		if got := query[string](t, db, q); got != want {
			t.Errorf("%s = %q, want %q", q, got, want)
		}
	}
}

// TestApplyPostgresTableNamedExcluded applies leftovers, whose table is named excluded like the row
// ON CONFLICT DO UPDATE proposes for insertion, and checks that a changed value is restored.
func TestApplyPostgresTableNamedExcluded(t *testing.T) {
	db := openDB(t, edgeDDL)
	if err := edgecases.ApplyMasterData(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `UPDATE excluded SET value = 'changed'`); err != nil {
		t.Fatal(err)
	}
	if err := edgecases.ApplyMasterData(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	if got := query[string](t, db, `SELECT value FROM excluded WHERE name = 'a'`); got != "kept" {
		t.Errorf("value = %q, want kept", got)
	}
}
