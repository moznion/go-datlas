package fixture_test

import (
	"testing"
	"time"

	"github.com/moznion/go-datlas/example/fixture"
	"github.com/moznion/go-datlas/example/model"
)

func TestFactory(t *testing.T) {
	now := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	f := fixture.New(fixture.WithSeed(42), fixture.WithNow(now), fixture.WithT(t))

	u := f.User()
	if u.ID == (model.UUID{}) || u.Email == "" || u.Name == "" {
		t.Fatalf("fields were not generated: %+v", u)
	}
	if u.Status != "active" && u.Status != "suspended" {
		t.Fatalf("unexpected status %q", u.Status)
	}
	if u.Age == nil || *u.Age < 18 || *u.Age > 99 {
		t.Fatalf("unexpected age %v", u.Age)
	}
	if !u.CreatedAt.Equal(now) {
		t.Fatalf("CreatedAt = %v, want %v", u.CreatedAt, now)
	}
	if u.Note != "" {
		t.Fatalf("untagged field must stay zero, got %q", u.Note)
	}
	if u.Code != "user-1" || f.User().Code != "user-2" {
		t.Fatalf("unexpected sequence: %q", u.Code)
	}
	if len(u.Tags) != 2 || u.Tags[0] == "" {
		t.Fatalf("unexpected tags %v", u.Tags)
	}
	if u.Company.ID == (model.UUID{}) || u.Company.Name == "" {
		t.Fatalf("nested company was not generated: %+v", u.Company)
	}
	if f.Seed() != 42 {
		t.Fatalf("Seed() = %d", f.Seed())
	}
}

func TestFactoryDeterministic(t *testing.T) {
	a := fixture.New(fixture.WithSeed(7)).Users(5)
	b := fixture.New(fixture.WithSeed(7)).Users(5)
	for i := range a {
		if a[i].ID != b[i].ID || a[i].Email != b[i].Email || a[i].Company.ID != b[i].Company.ID {
			t.Fatalf("same seed produced different users: %+v != %+v", a[i], b[i])
		}
	}
}

func TestFactoryOverrides(t *testing.T) {
	f := fixture.New()
	us := f.Users(3, func(u *model.User) { u.Email = "fixed@example.test" })
	if len(us) != 3 {
		t.Fatalf("len = %d, want 3", len(us))
	}
	for _, u := range us {
		if u.Email != "fixed@example.test" {
			t.Fatalf("override not applied: %q", u.Email)
		}
	}
	if us[0].ID == us[1].ID {
		t.Fatal("users share an ID")
	}
}

func TestFactoryPolicyFile(t *testing.T) {
	now := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	f := fixture.New(fixture.WithSeed(42), fixture.WithNow(now))

	a := f.Account()
	if a.ID == ([16]byte{}) || a.Email == "" || !a.CreatedAt.Equal(now) {
		t.Fatalf("fields were not generated: %+v", a)
	}
	if !a.DisplayName.Valid || a.DisplayName.String == "" {
		t.Fatalf("DisplayName was not wrapped: %+v", a.DisplayName)
	}
	if !a.Plan.Valid || (a.Plan.V != "free" && a.Plan.V != "pro") {
		t.Fatalf("unexpected plan %+v", a.Plan)
	}
	if a.DeletedAt.Valid {
		t.Fatalf("DeletedAt must stay zero, got %+v", a.DeletedAt)
	}
	if len(f.Accounts(2)) != 2 {
		t.Fatal("Accounts did not build 2 values")
	}
}

func TestFactoryHandWritten(t *testing.T) {
	f := fixture.New()
	if u := f.SuspendedUser(); u.Status != model.StatusSuspended || u.Email == "" {
		t.Fatalf("unexpected user %+v", u)
	}
	if a := f.DeletedAccount(); !a.DeletedAt.Valid || a.Email == "" {
		t.Fatalf("unexpected account %+v", a)
	}
}
