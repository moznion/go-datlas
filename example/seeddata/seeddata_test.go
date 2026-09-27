package seeddata_test

import (
	"slices"
	"testing"

	"github.com/moznion/go-datlas/example/seeddata"
)

func TestSeedData(t *testing.T) {
	if len(seeddata.LocalUsers()) != 2 || len(seeddata.DemoUsers()) != 1 {
		t.Fatalf("unexpected seed data: %+v %+v", seeddata.LocalUsers(), seeddata.DemoUsers())
	}
	bob, ok := seeddata.LocalUserByEmail("bob@example.test")
	if !ok || bob.JoinedAt.UTC().Hour() != 0 || bob.Role != "member" {
		t.Fatalf("unexpected bob: %+v", bob)
	}
}

func TestSeedFor(t *testing.T) {
	if envs := seeddata.SeedEnvironments(); !slices.Equal(envs, []string{"demo", "local"}) {
		t.Fatalf("SeedEnvironments() = %v", envs)
	}
	local, ok := seeddata.SeedFor("local")
	if !ok || len(local.Users) != 2 || len(local.Companies) != 1 {
		t.Fatalf("SeedFor(local) = %+v, %v", local, ok)
	}
	if _, ok := seeddata.SeedFor("production"); ok {
		t.Fatal("SeedFor(production) found seed data")
	}
}
