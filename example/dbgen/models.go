// Package dbgen stands in for code generated from DDL by a tool such as sqletch or sqlc.
// Its types cannot carry datlas annotations: fixture/fixture.yaml gives their fixture policy instead.
package dbgen

import (
	"database/sql"
	"time"
)

// InsertAccountParams are the parameters of an INSERT query.
type InsertAccountParams struct {
	ID          [16]byte
	Email       string
	DisplayName sql.NullString
	Plan        sql.Null[string]
	CreatedAt   time.Time
	DeletedAt   sql.NullTime
}

// InsertMembershipParams are the parameters of an INSERT query; AccountID is a foreign key.
type InsertMembershipParams struct {
	ID        [16]byte
	AccountID [16]byte
	Role      string
}
