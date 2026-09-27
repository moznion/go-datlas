# datlas example

A small application that uses all three kinds of generated data.
It is a separate module that runs datlas from this repository as a Go tool
(`tool github.com/moznion/go-datlas/cmd/datlas` in [go.mod](go.mod), with a `replace` to `../`).

## Layout

| Directory | Kind | Contents |
| --- | --- | --- |
| [model/](model) | input | Domain types annotated with `//datlas:fixture` and `//datlas:enum` |
| [dbgen/](dbgen) | input | Stands in for structs generated from DDL (sqletch, sqlc); not annotated |
| [fixture/](fixture) | Fixture | Factory for `model` and `dbgen` types, plus hand-written methods |
| [masterdata/](masterdata) | Master Data | Roles, permissions and plans compiled from YAML |
| [seeddata/](seeddata) | Seed Data | Companies and users per environment (`local`, `demo`) |

Files named `zz_*_gen.go` are generated. Everything else is written by hand.

## Running

```sh
cd example
go generate ./...   # regenerate every zz_*_gen.go
go test ./...
```

To check in CI that generated files are up to date, add `-check` to the commands in the
`go:generate` lines, e.g. `go tool datlas master -check` in `masterdata/`.

## Fixture

`fixture/doc.go` generates the factory from two sources:

```go
//go:generate go tool datlas fixture -config fixture.yaml ../model
```

### Annotated types: model/

[model/model.go](model/model.go) shows most struct tags:

- `User` uses `uuid`, `email`, `name`, `enum` (`Status` is a `//datlas:enum` type), `int` with a range
  on a pointer, `seq` with a prefix, a slice with `count=2`, and `nested` for `Company`.
- The embedded `Timestamps` struct is untagged, so its tagged fields are generated too.
- `Note` is untagged and stays empty.

```go
f := fixture.New(fixture.WithSeed(42), fixture.WithNow(now), fixture.WithT(t))
u := f.User()
us := f.Users(3, func(u *model.User) { u.Email = "fixed@example.test" })
```

### Types that cannot be annotated: dbgen/ and fixture.yaml

`dbgen.InsertAccountParams` plays the role of a generated struct: annotations added to it would be lost
on regeneration. [fixture/fixture.yaml](fixture/fixture.yaml) gives its policy instead:

```yaml
types:
  - type: github.com/moznion/go-datlas/example/dbgen.InsertAccountParams
    name: Account
    fields:
      ID: uuid
      Email: email
      DisplayName: name,wrap=nullString
      Plan: oneof=free|pro,wrap=null
      CreatedAt: now
      DeletedAt: "-"
```

- Every field is listed. If a field is added to `InsertAccountParams`, generation fails until the policy
  mentions it. `"-"` leaves `DeletedAt` at its zero value.
- `name: Account` names the methods `Account` and `Accounts` instead of `InsertAccountParams`.
- `wrap=` turns generated values into nullable ones through functions in
  [fixture/wrap.go](fixture/wrap.go):
  `nullString` builds a `sql.NullString`, and the generic `null` builds `sql.Null[string]`
  (called as `null[string](...)`, with the type argument taken from the field type).

### Hand-written methods: fixture/scenario.go

[fixture/scenario.go](fixture/scenario.go) adds methods to the generated `Factory` by hand.
They build on generated methods:

```go
func (f *Factory) SuspendedUser() model.User {
	return f.User(func(u *model.User) { u.Status = model.StatusSuspended })
}
```

`DeletedAccount` also uses `f.Faker()`, which shares the seed and the clock with generated methods.
A generated method with the same name as a hand-written one fails generation.

## Master Data

[masterdata/schema.go](masterdata/schema.go) declares three datasets. Each compiles `<name>.yaml`
in the same directory:

- `roles`: a key (`master:"key"`) and a `datlas:"nonzero"` name.
- `permissions`: a composite key whose `role` must exist in `roles` (`master:"key,ref=roles"`).
- `plans`: durations, slices, a pointer, a nested struct, an optional field (`omitempty`) and a
  `//datlas:enum` field. [plans.yaml](masterdata/plans.yaml) reuses a record with an anchor and a merge key.

```go
admin, ok := masterdata.RoleByCode("admin")
_, ok = masterdata.PermissionByRoleAndAction("admin", "write")
plans := masterdata.Plans() // a fresh copy; modifying it does not change the master data
```

## Seed Data

[seeddata/schema.go](seeddata/schema.go) declares `companies` and `users`.
Each directory is an environment: `local/users.yaml` and `demo/users.yaml` share the `UserSeed` schema.

- `UserSeed.CompanyKey` must refer to a company of the same environment (`seed:"ref=companies"`).
- `UserSeed.Role` must refer to a master data role
  (`seed:"ref=github.com/moznion/go-datlas/example/masterdata.roles"`).
- `joined_at` accepts RFC 3339 or a date (`2024-04-01`, read as UTC).

```go
alice, ok := seeddata.LocalUserByEmail("alice@example.test")
local, ok := seeddata.SeedFor("local") // every dataset of an environment
envs := seeddata.SeedEnvironments()    // ["demo", "local"]
```
