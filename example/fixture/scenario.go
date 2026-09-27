package fixture

import (
	"github.com/moznion/go-datlas/example/dbgen"
	"github.com/moznion/go-datlas/example/model"
)

// Hand-written factory methods live next to the generated ones and can build on them.

// SuspendedUser returns a new suspended model.User.
func (f *Factory) SuspendedUser() model.User {
	return f.User(func(u *model.User) { u.Status = model.StatusSuspended })
}

// DeletedAccount returns a new dbgen.InsertAccountParams of a deleted account.
func (f *Factory) DeletedAccount() dbgen.InsertAccountParams {
	return f.Account(func(a *dbgen.InsertAccountParams) {
		a.DeletedAt.Time, a.DeletedAt.Valid = f.Faker().Past(), true
	})
}
