package naming

import "testing"

func TestCamel(t *testing.T) {
	for in, want := range map[string]string{
		"roles":       "Roles",
		"local_users": "LocalUsers",
		"e2e":         "E2e",
		"demo-users":  "DemoUsers",
		"":            "",
	} {
		if got := Camel(in); got != want {
			t.Errorf("Camel(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPluralSingular(t *testing.T) {
	for singular, plural := range map[string]string{
		"User":     "Users",
		"Company":  "Companies",
		"Key":      "Keys",
		"Address":  "Addresses",
		"Box":      "Boxes",
		"Match":    "Matches",
		"Role":     "Roles",
		"Category": "Categories",
		"Movie":    "Movies",
		"Status":   "Statuses",
		"Bus":      "Buses",
		"Person":   "People",
		"House":    "Houses",
		"Series":   "Series",
		"Index":    "Indices",
		"Analysis": "Analyses",
		"Quiz":     "Quizzes",
		// Only the last word of an identifier is inflected.
		"UserStatus":  "UserStatuses",
		"AdminPerson": "AdminPeople",
		"Sale":        "Sales",
		"Minute":      "Minutes",
	} {
		if got := Plural(singular); got != plural {
			t.Errorf("Plural(%q) = %q, want %q", singular, got, plural)
		}
		if got := Singular(plural); got != singular {
			t.Errorf("Singular(%q) = %q, want %q", plural, got, singular)
		}
	}
	for _, s := range []string{"Status", "Data", "Class"} {
		if got := Singular(s); got != s {
			t.Errorf("Singular(%q) = %q, want unchanged", s, got)
		}
	}
}
