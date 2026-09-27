// Package naming derives Go identifiers from dataset and type names.
package naming

import (
	"strings"
	"unicode"
)

// Camel converts a snake_case or kebab-case name into an exported CamelCase identifier.
// For example, "local_users" becomes "LocalUsers".
func Camel(name string) string {
	var b strings.Builder
	for _, part := range strings.FieldsFunc(name, func(r rune) bool {
		return r == '_' || r == '-' || r == '.' || unicode.IsSpace(r)
	}) {
		rs := []rune(part)
		rs[0] = unicode.ToUpper(rs[0])
		b.WriteString(string(rs))
	}
	return b.String()
}

// IsIdentifier reports whether s is a valid Go identifier.
func IsIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if r != '_' && !unicode.IsLetter(r) && (i == 0 || !unicode.IsDigit(r)) {
			return false
		}
	}
	return true
}

// irregulars lists singular/plural pairs the suffix rules get wrong, in lower case.
var irregulars = [][2]string{
	{"person", "people"}, {"child", "children"}, {"man", "men"}, {"woman", "women"},
	{"foot", "feet"}, {"tooth", "teeth"}, {"mouse", "mice"}, {"goose", "geese"},
	{"index", "indices"}, {"matrix", "matrices"}, {"vertex", "vertices"},
	{"analysis", "analyses"}, {"crisis", "crises"}, {"thesis", "theses"},
	{"criterion", "criteria"}, {"phenomenon", "phenomena"}, {"data", "data"}, {"media", "media"},
	{"series", "series"}, {"species", "species"}, {"news", "news"}, {"equipment", "equipment"},
	{"information", "information"}, {"sheep", "sheep"}, {"fish", "fish"},
	{"quiz", "quizzes"},
	{"movie", "movies"}, {"cookie", "cookies"}, {"zombie", "zombies"}, {"rookie", "rookies"},
	{"calorie", "calories"}, {"prairie", "prairies"}, {"selfie", "selfies"}, {"smoothie", "smoothies"},
	{"brownie", "brownies"}, {"sortie", "sorties"}, {"goalie", "goalies"}, {"genie", "genies"},
	{"pie", "pies"}, {"tie", "ties"}, {"lie", "lies"}, {"die", "dies"},
	{"status", "statuses"}, {"bus", "buses"}, {"virus", "viruses"}, {"bonus", "bonuses"},
	{"campus", "campuses"}, {"census", "censuses"}, {"corpus", "corpora"}, {"focus", "foci"},
	{"radius", "radii"}, {"cactus", "cacti"}, {"fungus", "fungi"}, {"nucleus", "nuclei"},
	{"stimulus", "stimuli"}, {"syllabus", "syllabi"}, {"consensus", "consensuses"},
	{"octopus", "octopuses"}, {"walrus", "walruses"}, {"alias", "aliases"}, {"canvas", "canvases"},
	{"house", "houses"}, {"cause", "causes"}, {"course", "courses"}, {"excuse", "excuses"},
	{"fuse", "fuses"}, {"use", "uses"}, {"case", "cases"}, {"base", "bases"}, {"phase", "phases"},
	{"database", "databases"}, {"purchase", "purchases"}, {"release", "releases"}, {"response", "responses"},
	{"license", "licenses"}, {"expense", "expenses"}, {"clause", "clauses"},
}

// replaceLastWord replaces the last word of s when it equals from (ignoring case),
// keeping the capitalisation of its first letter. A word starts at the beginning of s
// or at an upper case letter.
func replaceLastWord(s, from, to string) (string, bool) {
	if len(s) < len(from) || !strings.EqualFold(s[len(s)-len(from):], from) {
		return "", false
	}
	start := len(s) - len(from)
	if start > 0 && !unicode.IsUpper(rune(s[start])) {
		return "", false
	}
	if unicode.IsUpper(rune(s[start])) {
		to = strings.ToUpper(to[:1]) + to[1:]
	}
	return s[:start] + to, true
}

// Plural returns the English plural form of an identifier, e.g. "User" -> "Users".
// Rules cover regular words and a table of common irregular ones; directives accept
// explicit names where they are still wrong.
func Plural(s string) string {
	for _, p := range irregulars {
		if r, ok := replaceLastWord(s, p[0], p[1]); ok {
			return r
		}
	}
	lower := strings.ToLower(s)
	switch {
	case strings.HasSuffix(lower, "y") && len(s) > 1 && !isVowel(lower[len(lower)-2]):
		return s[:len(s)-1] + "ies"
	case strings.HasSuffix(lower, "s"), strings.HasSuffix(lower, "x"), strings.HasSuffix(lower, "z"),
		strings.HasSuffix(lower, "ch"), strings.HasSuffix(lower, "sh"):
		return s + "es"
	default:
		return s + "s"
	}
}

// Singular returns the English singular form of an identifier, e.g. "Companies" -> "Company".
// Identifiers that do not look plural are returned unchanged.
func Singular(s string) string {
	for _, p := range irregulars {
		if r, ok := replaceLastWord(s, p[1], p[0]); ok {
			return r
		}
	}
	lower := strings.ToLower(s)
	switch {
	case strings.HasSuffix(lower, "ies") && len(s) > 3:
		return s[:len(s)-3] + "y"
	case strings.HasSuffix(lower, "sses"), strings.HasSuffix(lower, "xes"), strings.HasSuffix(lower, "zes"),
		strings.HasSuffix(lower, "ches"), strings.HasSuffix(lower, "shes"):
		return s[:len(s)-2]
	case strings.HasSuffix(lower, "ss"), strings.HasSuffix(lower, "us"), strings.HasSuffix(lower, "is"):
		return s
	case strings.HasSuffix(lower, "s") && len(s) > 1:
		return s[:len(s)-1]
	default:
		return s
	}
}

func isVowel(c byte) bool {
	return strings.IndexByte("aeiou", c) >= 0
}
