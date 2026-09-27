// Package seeddata holds the per-environment seed data of the example application.
package seeddata

import "time"

//go:generate go tool datlas seed

//datlas:seed companies
type CompanySeed struct {
	Key  string `yaml:"key" seed:"key"`
	Name string `yaml:"name"`
}

//datlas:seed users
type UserSeed struct {
	Email      string    `yaml:"email" seed:"key"`
	CompanyKey string    `yaml:"company" seed:"ref=companies"`
	Role       string    `yaml:"role" seed:"ref=github.com/moznion/go-datlas/example/masterdata.roles"`
	JoinedAt   time.Time `yaml:"joined_at"`
}
