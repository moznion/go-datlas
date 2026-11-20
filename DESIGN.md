# Typed Test Data Generator Design

## Goal

This project provides a single code generation tool for three different kinds of data:

- **Fixture**: dynamically generated objects for tests.
- **Master Data**: declarative application data that is part of the application's specification.
- **Seed Data**: declarative environment initialization data (local, demo, e2e, etc.).

Although these three concepts share a similar implementation, they have different semantics and therefore must remain independent.

The generator should provide a common implementation while preserving this separation.

---

# Non-goals

The generator is **not** responsible for:

- ORM integration
- Migration generation (DDL)
- Transaction management
- SQL for fixtures

The generator produces typed Go code. For Master Data and Seed Data mapped to a table
(`table=<name>`), that code includes the statements writing the data to a database
(see [Applying Data](#applying-data)): those statements follow from the schema almost uniquely,
so making every application write them would halve the value of the generator.
The database connection and the transaction stay the application's.

---

# Overall Architecture

```
                         Go source
                             │
                             ▼
                  package / AST / go/types
                             │
                             ▼
                      datlas generator
                             │
          ┌──────────────────┼──────────────────┐
          │                  │                  │
          ▼                  ▼                  ▼
      Fixture           Master Data        Seed Data
          │                  │                  │
     Go generation      YAML compile      YAML compile
          │                  │                  │
          ▼                  ▼                  ▼
zz_fixture_gen.go  zz_masterdata_gen.go zz_seeddata_gen.go
```

---

# Concepts

## Fixture

Purpose:

- test data generation
- random values
- current timestamps
- deterministic random when requested

Characteristics:

- dynamic
- generated during test execution
- never stored as YAML

Example:

```go
//datlas:fixture
type User struct {
    ID        uuid.UUID `fixture:"uuid"`
    Email     string    `fixture:"email"`
    CreatedAt time.Time `fixture:"now"`
}
```

Generated API:

```go
factory := fixture.New(
    fixture.WithSeed(42),
)

user := factory.User()

// Values can be overridden after generation.
admin := factory.User(func(u *User) {
    u.Email = "admin@example.test"
})

users := factory.Users(3)
```

Fields without a `fixture` tag keep their zero value.
A field of another fixture type can be built with `fixture:"nested"`,
and slice fields get `count=N` values.

---

## Master Data

Master Data represents application specification.

Examples:

- roles
- permissions
- subscription plans
- questionnaire definitions

Characteristics:

- declarative
- immutable from application's perspective
- environment independent

Example:

```go
//datlas:master roles
type RoleDefinition struct {
    Code string `yaml:"code" master:"key"`
    Name string `yaml:"name"`
}
```

YAML:

```yaml
- code: admin
  name: Administrator

- code: member
  name: Member
```

Generated:

```go
func Roles() []RoleDefinition

func RoleByCode(key string) (RoleDefinition, bool)
```

Master Data is immutable from the application's perspective:
every call returns a new copy, so no caller can modify the data seen by others.

---

## Seed Data

Seed Data represents environment initialization.

Examples:

- demo company
- local users
- demo questionnaires
- e2e accounts

Characteristics:

- declarative
- environment dependent
- not application specification

Example:

```go
//datlas:seed users
type UserSeed struct {
    Email      string `yaml:"email" seed:"key"`
    CompanyKey string `yaml:"company"`
}
```

The schema is shared by all environments.
Each environment has its own YAML file.

YAML (`seeddata/local/users.yaml`):

```yaml
- email: alice@example.test
  company: acme

- email: bob@example.test
  company: acme
```

Generated:

```go
func LocalUsers() []UserSeed

func LocalUserByEmail(key string) (UserSeed, bool)

// seeddata/demo/users.yaml
func DemoUsers() []UserSeed

func DemoUserByEmail(key string) (UserSeed, bool)

// all environments
func SeedEnvironments() []string

type SeedSet struct {
    Users []UserSeed
    ...
}

func SeedFor(env string) (SeedSet, bool)
```

---

# Responsibilities

The generator owns:

- parsing Go packages
- parsing directives
- parsing YAML
- validating YAML
- generating Go source
- formatting generated code

The application owns:

- the database schema (DDL) and the connection
- transaction management
- mapping definitions into DB models, when they are not mapped to a table

---

# Why Master Data and Seed Data are Separate

Although both are declarative YAML, they serve different purposes.

Master Data answers:

> What data defines the application?

Seed Data answers:

> What data should exist in this environment?

Example:

```
Master Data
-------------
Role(admin)
Plan(pro)

Seed Data (local)
-----------------
Company(ACME)
User(alice@example.test)
```

Alice is not part of the application's specification.

Admin role is.

Therefore they must remain separate concepts.

---

# Data Flow

## Fixture

```
Go struct
      │
      ▼
Fixture Generator
      │
      ▼
zz_fixture_gen.go
      │
      ▼
Tests
```

---

## Master Data

```
Go schema
      │
      │
masterdata/*.yaml
      │
      ▼
Validation
      │
      ▼
zz_masterdata_gen.go
      │
      ▼
Application
```

---

## Seed Data

```
Go schema
      │
      │
seeddata/local/*.yaml
      │
      ▼
Validation
      │
      ▼
zz_seeddata_gen.go
      │
      ▼
Application
```

---

# Database Model

The generator intentionally does not understand database models.

Instead:

```
Master Definition
        │
        ▼
Application Mapping
        │
        ▼
DB Model
        │
        ▼
Database
```

Example:

```
RoleDefinition
--------------
Code
Name

↓

Role
----
UUID
Code
Name
CreatedAt
UpdatedAt
```

The generator is unaware of the lower half.

---

# Applying Data

Datasets mapped to a table (`//datlas:master roles table=roles`) are applied by generated functions
that take the database from the application:

```
ApplyMasterData(ctx, db)

ApplySeedData(ctx, db, "local")
```

`db` is anything with `ExecContext` (`*sql.DB`, `*sql.Tx`, `*sql.Conn`), so the application decides
on transactions. The statements are generated for one SQL dialect (`-dialect`, PostgreSQL first);
dialects implement one interface in the generator, so others can be added without changing the rest.

- Records are upserted by their logical key; records without one are inserted.
- Master Data is the specification, so rows whose keys are not in it are deleted (prune), unless
  `prune=false`. Seed data tables also hold rows the application creates, so seed data prunes only
  with `prune=true`.
- Columns are named by `db` tags or YAML keys. Values that are not scalars are stored as JSON.

Datasets without a table are not written; the application consumes their generated Go values.
`SeedFor(env)` gives the seed data of an environment by name.

References between datasets are known at generation time, so the generator also emits the
order in which datasets can be applied: `MasterDatasets()` and `SeedDatasets()` list the dataset
names so that every dataset comes after the datasets it refers to. The fields of `SeedSet`
follow this order. Datasets in a reference cycle cannot be ordered and are listed adjacently.

---

# Identity

Master Data must define a logical identity.

Example:

```go
Code string `yaml:"code" master:"key"`
```

This logical key is used to detect duplicates during generation.
Keys of types implementing `encoding.TextUnmarshaler` (e.g. UUIDs) are compared by decoded value,
since different texts can denote the same value; the program validating such values does it.

The generator should fail if duplicate logical keys are found.

Seed Data may also define logical identifiers when necessary:

```go
Email string `yaml:"email" seed:"key"`
```

Tagging several fields makes a composite key:

```go
Role   string `yaml:"role" master:"key"`
Action string `yaml:"action" master:"key"`
```

When a logical key is defined, a lookup function by key is generated
(e.g. `RoleByCode`, `LocalUserByEmail`, `PermissionByRoleAndAction`).

Master Data is part of the specification, so application code refers to individual records.
For a single-field key of a string or integer type, a constant is generated per record
(`RoleCodeAdmin = "admin"`), turning a removed or renamed key into a compile error.
`//datlas:master roles consts=false` disables them.

Records may refer to the logical key of another dataset:

```go
CompanyKey string `yaml:"company" seed:"ref=companies"`
Role       string `yaml:"role" seed:"ref=example.com/app/masterdata.roles"`
```

Master Data refers to Master Data of the same package.
Seed Data refers to Seed Data of the same environment,
or to Master Data of another package by import path.
The generator fails if a referenced key does not exist.

Database UUIDs are intentionally not part of the declarative files.

---

# Validation

YAML is decoded in strict mode.

Generation fails for:

- unknown fields
- missing required fields
- invalid types
- malformed YAML
- duplicate logical keys
- duplicate values of fields tagged `datlas:"unique"` or `datlas:"unique=<group>"` (for `encoding.TextUnmarshaler` types, compared by decoded value)
- references to missing keys
- values outside of an enum
- empty values of fields tagged `datlas:"nonzero"` (for `encoding.TextUnmarshaler` types, values that decode to the zero value),
  and empty elements of fields tagged `datlas:"elem=nonzero"`
- YAML files that no schema declares

Every field is required unless it is a pointer or its `yaml` tag has `omitempty`.

Validation happens during code generation.

No YAML parsing happens at runtime.

Values of types implementing `encoding.TextUnmarshaler` (e.g. decimals) are the one exception to
compile-time literals: the generator validates them by building and running a small program that
calls `UnmarshalText`, and the generated code decodes the already validated text at runtime.

---

# Environment Support

Master Data:

```
masterdata/
    roles.yaml
    permissions.yaml
```

Seed Data:

```
seeddata/
    local/
        users.yaml

    demo/
        users.yaml

    e2e/
        users.yaml
```

Master Data is environment independent.

Seed Data is environment dependent.

Each directory under `seeddata/` is an environment.
A single `//datlas:seed users` schema applies to `users.yaml` in every environment,
and one accessor is generated per environment (`LocalUsers`, `DemoUsers`, `E2eUsers`).
Environments without a file for a dataset get an empty accessor.

---

# Fixture Design

Generated fixture API is instance-based.

```
factory := fixture.New(
    fixture.WithSeed(42),
)
```

instead of global functions.

The factory owns:

- RNG
- Faker
- Clock

This enables deterministic testing.

---

# Generator Directives

Fixture:

```go
//datlas:fixture
```

Master Data:

```go
//datlas:master roles
```

Seed Data:

```go
//datlas:seed users
```

Field-level fixture generation uses struct tags.

Example:

```go
ID        uuid.UUID `fixture:"uuid"`
Email     string    `fixture:"email"`
CreatedAt time.Time `fixture:"now"`
```

Master Data and Seed Data use ordinary `yaml` tags.
The logical key is marked with `master:"key"` or `seed:"key"`,
and references with `master:"ref=..."` or `seed:"ref=..."`.

Enum types restrict YAML values to their constants:

```go
//datlas:enum
type Tier string
```

The directive argument is the dataset name, which determines the YAML file name
(`roles` → `masterdata/roles.yaml`, `users` → `seeddata/<env>/users.yaml`).

Directives accept options to override generated names:

```go
//datlas:master people singular=Person   // PersonByCode instead of the guessed singular
//datlas:fixture name=AdminUser          // f.AdminUser() and f.AdminUsers()
```

The generator is typically run through `go:generate`:

```go
//go:generate go tool datlas fixture ../model
//go:generate go tool datlas master
//go:generate go tool datlas seed
```

---

# Project Layout

```
cmd/
    datlas/

internal/
    generator/
        loader/
        fixture/
        declarative/
        output/
        naming/

fixturekit/

masterdata/
    schema.go
    roles.yaml
    permissions.yaml
    zz_masterdata_gen.go

seeddata/
    schema.go

    local/
        users.yaml
        companies.yaml

    demo/
        users.yaml

    zz_seeddata_gen.go

fixture/
    zz_fixture_gen.go
```

`fixturekit/` is the runtime of generated fixture factories (RNG, Faker, Clock).
It depends only on the standard library. It lives in the same Go module as the generator,
so the generated code and the runtime it calls always come from the same version.

---

# Internal Pipeline

```
Go packages
        │
        ▼
AST + go/types
        │
        ▼
Directive Analyzer
        │
        ▼
Intermediate Representation
        │
        ▼
Code Renderer
        │
        ▼
go/format
        │
        ▼
Generated Go files
```

Fixture and Declarative Data share the package loading infrastructure.

Their semantic analyzers remain independent.

---

# Intermediate Representation

Fixture:

```
FixtureType
 ├── Name
 ├── Fields
 └── GeneratorPolicy
```

Declarative Data:

```
Dataset
 ├── Kind
 ├── Name
 ├── Schema
 ├── YAML Files
 └── Records
```

`Kind` is one of:

- MasterData
- SeedData

---

# Future Extensions

Possible future features:

- schema versioning
- YAML include/import
- Faker plugins
- generated validation reports

Explicitly out of scope:

- ORM integration
- migration generation
- association graph generation (fields can only be linked to values the test supplies; see `from` and `ref` in the README)
- callback system similar to FactoryBot

---

# Design Principles

1. Go structs are the single source of type information.
2. Fixture, Master Data, and Seed Data are distinct concepts.
3. The generator produces only typed Go code.
4. Runtime YAML parsing is eliminated.
5. Database persistence remains an application concern.
6. All generated APIs should feel like ordinary handwritten Go code.
