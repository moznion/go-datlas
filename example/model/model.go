// Package model contains the domain types of the example application.
package model

import "time"

// UUID stands in for a UUID type such as github.com/google/uuid.UUID.
type UUID [16]byte

// Status is the state of a user account.
//
//datlas:enum
type Status string

const (
	StatusActive    Status = "active"
	StatusSuspended Status = "suspended"
)

// Timestamps is embedded by persisted types.
type Timestamps struct {
	CreatedAt time.Time `fixture:"now"`
	UpdatedAt time.Time `fixture:"now"`
}

//datlas:fixture
type User struct {
	ID      UUID     `fixture:"uuid"`
	Email   string   `fixture:"email"`
	Name    string   `fixture:"name"`
	Status  Status   `fixture:"enum"`
	Age     *int     `fixture:"int,min=18,max=99"`
	Code    string   `fixture:"seq,prefix=user-"`
	Tags    []string `fixture:"word,count=2"`
	Company Company  `fixture:"nested"`
	// CompanyID is copied from Company, so that the two agree.
	CompanyID UUID `fixture:"from=Company.ID"`
	Timestamps
	Note string
}

//datlas:fixture
type Company struct {
	ID   UUID   `fixture:"uuid"`
	Name string `fixture:"word,unique"`
}
