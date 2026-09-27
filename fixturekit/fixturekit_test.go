package fixturekit

import (
	"fmt"
	"regexp"
	"testing"
	"time"
)

func TestSeedIsDeterministic(t *testing.T) {
	a, b := New(WithSeed(42)), New(WithSeed(42))
	for range 100 {
		if x, y := a.Email(), b.Email(); x != y {
			t.Fatalf("same seed produced different values: %q != %q", x, y)
		}
		if x, y := a.UUIDString(), b.UUIDString(); x != y {
			t.Fatalf("same seed produced different values: %q != %q", x, y)
		}
	}
	if New(WithSeed(1)).UUID() == New(WithSeed(2)).UUID() {
		t.Fatal("different seeds produced the same UUID")
	}
}

func TestUUID(t *testing.T) {
	g := New()
	re := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	for range 100 {
		if s := g.UUIDString(); !re.MatchString(s) {
			t.Fatalf("not a version 4 UUID: %s", s)
		}
	}
}

func TestRanges(t *testing.T) {
	g := New(WithSeed(1))
	for range 1000 {
		if v := g.Int64Range(-3, 3); v < -3 || v > 3 {
			t.Fatalf("Int64Range out of range: %d", v)
		}
		if v := g.Uint64Range(5, 6); v < 5 || v > 6 {
			t.Fatalf("Uint64Range out of range: %d", v)
		}
		if v := g.Float64Range(1, 2); v < 1 || v >= 2 {
			t.Fatalf("Float64Range out of range: %g", v)
		}
	}
	// Full ranges must not panic.
	g.Int64Range(-1<<63, 1<<63-1)
	g.Uint64Range(0, ^uint64(0))
}

func TestUniqueValues(t *testing.T) {
	g := New(WithSeed(1))
	seen := map[string]bool{}
	for range 1000 {
		e := g.Email()
		if seen[e] {
			t.Fatalf("duplicate email %q", e)
		}
		seen[e] = true
	}
}

func TestClock(t *testing.T) {
	now := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	g := New(WithNow(now))
	if !g.Now().Equal(now) {
		t.Fatalf("Now() = %v, want %v", g.Now(), now)
	}
	for range 100 {
		if p := g.Past(); !p.Before(now) || p.Before(now.AddDate(-1, 0, -1)) {
			t.Fatalf("Past() = %v is not within the last year", p)
		}
		if f := g.Future(); !f.After(now) || f.After(now.AddDate(1, 0, 1)) {
			t.Fatalf("Future() = %v is not within the next year", f)
		}
	}
}

func TestSeq(t *testing.T) {
	g := New()
	if a, b, c := g.Seq("a"), g.Seq("a"), g.Seq("b"); a != 1 || b != 2 || c != 1 {
		t.Fatalf("Seq = %d, %d, %d; want 1, 2, 1", a, b, c)
	}
}

func TestSeedFromEnvironment(t *testing.T) {
	t.Setenv(SeedEnv, "99")
	if a, b := New(), New(WithSeed(99)); a.Seed() != 99 || a.UUID() != b.UUID() {
		t.Fatalf("seed from %s was not used", SeedEnv)
	}
}

func TestSeedIsReportedOnFailure(t *testing.T) {
	tb := &fakeTB{failed: true}
	g := New(WithT(tb), WithSeed(7))
	for _, f := range tb.cleanups {
		f()
	}
	if g.Seed() != 7 || len(tb.logs) != 1 || tb.logs[0] != "fixture seed: 7 (rerun with DATLAS_SEED=7)" {
		t.Fatalf("unexpected logs: %q", tb.logs)
	}

	passed := &fakeTB{}
	New(WithT(passed))
	for _, f := range passed.cleanups {
		f()
	}
	if len(passed.logs) != 0 {
		t.Fatalf("seed reported for a passing test: %q", passed.logs)
	}
}

type fakeTB struct {
	failed   bool
	cleanups []func()
	logs     []string
}

func (f *fakeTB) Helper()           {}
func (f *fakeTB) Cleanup(fn func()) { f.cleanups = append(f.cleanups, fn) }
func (f *fakeTB) Failed() bool      { return f.failed }
func (f *fakeTB) Logf(format string, args ...any) {
	f.logs = append(f.logs, fmt.Sprintf(format, args...))
}

func TestTimePrecision(t *testing.T) {
	now := time.Date(2025, 1, 2, 3, 4, 5, 123456789, time.UTC)
	if got := New(WithNow(now)).Now(); got.Nanosecond() != 123456000 {
		t.Fatalf("default precision: got %v", got)
	}
	if got := New(WithNow(now), WithTimePrecision(0)).Now(); !got.Equal(now) {
		t.Fatalf("full precision: got %v", got)
	}
	if got := New(WithNow(now), WithTimePrecision(time.Second)).Now(); got.Nanosecond() != 0 {
		t.Fatalf("second precision: got %v", got)
	}
	if got := New().Now(); got != got.Round(0) {
		t.Fatal("monotonic clock reading was not stripped")
	}
}

func TestUniqueAcrossGenerators(t *testing.T) {
	seen := map[string]bool{}
	for range 1000 {
		g := New()
		for _, v := range []string{g.Username(), g.Email()} {
			if seen[v] {
				t.Fatalf("duplicate value %q across generators", v)
			}
			seen[v] = true
		}
	}
	a, b := New(WithSeed(3)), New(WithSeed(3))
	if a.Email() != b.Email() {
		t.Fatal("same seed must give the same email")
	}
}
