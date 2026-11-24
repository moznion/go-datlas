// Package edgecases holds datasets exercising edge cases of the generated SQL
// (an empty dataset, a schema-qualified table, records without a key); see ../dbapply_test.go.
package edgecases

//go:generate go tool datlas master -dir master
//go:generate go tool datlas seed -dir seed

// Flag has no records, so applying it deletes every row of its table.
//
//datlas:master flags table=config.flags
type Flag struct {
	Name string `yaml:"name" master:"key"`
}

// Event has no logical key, so its records are inserted on every application.
//
//datlas:seed events table=events
type Event struct {
	Kind    string            `yaml:"kind"`
	Payload map[string]string `yaml:"payload"`
}

// Tag prunes, and its file is empty: applying it deletes every row of its table.
//
//datlas:seed tags table=tags prune=true
type Tag struct {
	Name string `yaml:"name" seed:"key"`
}

// Grade shares its table with rows the application creates (company_id set), so it does not prune.
// Its label is written on insert only.
//
//datlas:master grades table=grades prune=false
type Grade struct {
	ID         string          `yaml:"id" master:"key"`
	Label      string          `yaml:"label" db:",insertonly"`
	Permission GradePermission `yaml:"permission"`
}

// GradePermission is written to a jsonb column.
type GradePermission struct {
	Actions []string `yaml:"actions" json:"actions"`
}
