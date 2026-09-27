// Package masterdata holds the master data of the example application.
package masterdata

import "time"

//go:generate go tool datlas master

//datlas:enum
type Tier string

const (
	TierFree     Tier = "free"
	TierBusiness Tier = "business"
)

//datlas:master roles
type RoleDefinition struct {
	Code string `yaml:"code" master:"key"`
	Name string `yaml:"name" datlas:"nonzero"`
}

//datlas:master permissions
type PermissionDefinition struct {
	Role   string `yaml:"role" master:"key,ref=roles"`
	Action string `yaml:"action" master:"key"`
}

//datlas:master plans
type PlanDefinition struct {
	Code        string        `yaml:"code" master:"key"`
	Name        string        `yaml:"name" datlas:"nonzero"`
	Tier        Tier          `yaml:"tier"`
	PriceYen    int           `yaml:"price_yen"`
	Trial       time.Duration `yaml:"trial"`
	Features    []string      `yaml:"features"`
	Description *string       `yaml:"description"`
	Limits      PlanLimits    `yaml:"limits"`
}

type PlanLimits struct {
	Seats   int `yaml:"seats"`
	Storage int `yaml:"storage_gb,omitempty"`
}
