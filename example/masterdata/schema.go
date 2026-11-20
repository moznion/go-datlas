// Package masterdata holds the master data of the example application.
package masterdata

import (
	"time"

	"github.com/moznion/go-datlas/example/model"
)

//go:generate go tool datlas master

//datlas:enum
type Tier string

const (
	TierFree     Tier = "free"
	TierBusiness Tier = "business"
)

//datlas:master roles table=roles
type RoleDefinition struct {
	Code string `yaml:"code" master:"key"`
	Name string `yaml:"name" datlas:"nonzero"`
}

//datlas:master permissions table=permissions
type PermissionDefinition struct {
	Role   string `yaml:"role" master:"key,ref=roles"`
	Action string `yaml:"action" master:"key"`
}

//datlas:master plans table=plans
type PlanDefinition struct {
	Code        string        `yaml:"code" master:"key"`
	Name        string        `yaml:"name" datlas:"nonzero,unique"`
	Tier        Tier          `yaml:"tier"`
	PriceYen    int           `yaml:"price_yen"`
	Trial       time.Duration `yaml:"trial"`
	Features    []string      `yaml:"features" datlas:"elem=nonzero"`
	Regions     []model.UUID  `yaml:"regions" master:"ref=regions" datlas:"nonzero,elem=nonzero"`
	Description *string       `yaml:"description"`
	Limits      PlanLimits    `yaml:"limits"`
}

type PlanLimits struct {
	Seats   int `yaml:"seats"`
	Storage int `yaml:"storage_gb,omitempty"`
}

// RegionDefinition is keyed by a type implementing encoding.TextUnmarshaler.
//
//datlas:master regions table=regions
type RegionDefinition struct {
	ID      model.UUID `yaml:"id" master:"key"`
	Code    string     `yaml:"code" datlas:"nonzero,unique"`
	Country string     `yaml:"country" datlas:"nonzero,unique=place"`
	Name    string     `yaml:"name" datlas:"nonzero,unique=place"`
}
