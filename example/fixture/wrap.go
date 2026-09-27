package fixture

import "database/sql"

// nullString and null build nullable values; fixture.yaml refers to them with wrap=.

func nullString(s string) sql.NullString { return sql.NullString{String: s, Valid: true} }

func null[T any](v T) sql.Null[T] { return sql.Null[T]{V: v, Valid: true} }
