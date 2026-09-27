package declarative

import (
	"errors"
	"fmt"
	"go/types"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"
	"github.com/goccy/go-yaml/token"

	"github.com/moznion/go-datlas/internal/generator/loader"
	"github.com/moznion/go-datlas/internal/generator/output"
)

// maxErrors bounds the number of errors reported for a single file.
const maxErrors = 50

// maxDepth guards against pathological nesting, e.g. through aliases.
const maxDepth = 256

// Record is a single compiled element of a dataset.
type Record struct {
	// Expr is the Go composite literal of the record, with its type elided.
	Expr string
	// Keys are the Go literals of the logical key fields, in declaration order.
	Keys []string
	// Refs are the values of reference fields, checked once every dataset is compiled.
	Refs []RefValue
	// Pos is the position of the record in its YAML file.
	Pos string
}

// RefValue is one value of a reference field.
type RefValue struct {
	Ref *Ref
	Lit string
	Pos string
}

// textCheck is a value of a type decoded with encoding.TextUnmarshaler.
// Such values can only be validated by running the user's code (see runTextChecks).
type textCheck struct {
	typ  types.Type
	text string
	pos  string
}

// compiler compiles YAML nodes into Go expressions, driven by the Go type expected at each node.
// All validation (strict fields, required fields, types, ranges, enums, constraints) happens here.
type compiler struct {
	path     string
	im       *output.Imports
	enums    *loader.Enums
	ptrFunc  string
	textFunc string
	usesPtr  bool
	usesText bool
	texts    []textCheck
	anchors  map[string][]anchorDef
	errs     []error
	errSeen  map[string]bool

	// keyVars are the logical key fields, which must not be zero.
	keyVars map[*types.Var]bool
	// refs maps reference fields to their reference.
	refs map[*types.Var]*Ref
	// capture collects the literals of the reference field being compiled.
	capture *[]RefValue
	// recordRefs collects the reference values of the record being compiled.
	recordRefs []RefValue
	// recordKeys collects the key literals of the record being compiled.
	recordKeys map[*types.Var]string
}

// anchorDef is a YAML anchor. An anchor name may be defined again; an alias refers to
// the closest definition before it.
type anchorDef struct {
	line, column int
	value        ast.Node
}

type compileError struct{ msg string }

func (e *compileError) Error() string { return e.msg }

func (c *compiler) errorf(n ast.Node, format string, args ...any) {
	loc := c.path
	if n != nil {
		if tk := n.GetToken(); tk != nil && tk.Position != nil {
			loc = fmt.Sprintf("%s:%d:%d", c.path, tk.Position.Line, tk.Position.Column)
		}
	}
	msg := loc + ": " + fmt.Sprintf(format, args...)
	// The same node can be examined more than once (e.g. an alias resolved from two places).
	if c.errSeen == nil {
		c.errSeen = map[string]bool{}
	}
	if c.errSeen[msg] {
		return
	}
	c.errSeen[msg] = true
	if len(c.errs) == maxErrors {
		c.errs = append(c.errs, &compileError{fmt.Sprintf("%s: too many errors", c.path)})
	}
	if len(c.errs) > maxErrors {
		return
	}
	c.errs = append(c.errs, &compileError{msg})
}

// compileFile parses src and compiles it as a sequence of values of schema.
func (c *compiler) compileFile(src []byte, schema Schema) []Record {
	c.keyVars = map[*types.Var]bool{}
	for _, k := range schema.Keys {
		c.keyVars[k] = true
	}
	c.refs = map[*types.Var]*Ref{}
	for i := range schema.Refs {
		c.refs[schema.Refs[i].Field] = &schema.Refs[i]
	}
	f, err := parser.ParseBytes(src, 0)
	if err != nil {
		c.errs = append(c.errs, fmt.Errorf("%s: malformed YAML:\n%w", c.path, err))
		return nil
	}
	if len(f.Docs) != 1 || f.Docs[0].Body == nil {
		c.errorf(nil, "expected exactly one YAML document containing a sequence of records, found %d", countDocs(f))
		return nil
	}
	body := f.Docs[0].Body
	c.collectAnchors(body)

	n, _ := c.resolve(body, 0)
	seq, ok := n.(*ast.SequenceNode)
	if !ok {
		c.errorf(n, "expected a sequence of records, got %s", describe(n))
		return nil
	}
	var records []Record
	for _, item := range seq.Values {
		c.recordRefs = nil
		c.recordKeys = map[*types.Var]string{}
		fields, ok := c.structFields(schema.Struct, item, 1, true)
		if !ok {
			continue
		}
		r := Record{Expr: "{\n" + joinFields(fields) + "}", Pos: c.pos(item), Refs: c.recordRefs}
		for _, k := range schema.Keys {
			r.Keys = append(r.Keys, c.recordKeys[k])
		}
		records = append(records, r)
	}
	return records
}

func countDocs(f *ast.File) int {
	n := 0
	for _, d := range f.Docs {
		if d.Body != nil {
			n++
		}
	}
	return n
}

func (c *compiler) pos(n ast.Node) string {
	if tk := n.GetToken(); tk != nil && tk.Position != nil {
		return fmt.Sprintf("%s:%d:%d", c.path, tk.Position.Line, tk.Position.Column)
	}
	return c.path
}

func (c *compiler) collectAnchors(root ast.Node) {
	c.anchors = map[string][]anchorDef{}
	ast.Walk(anchorCollector{c}, root)
}

type anchorCollector struct{ c *compiler }

func (a anchorCollector) Visit(n ast.Node) ast.Visitor {
	if anchor, ok := n.(*ast.AnchorNode); ok {
		name := anchor.Name.String()
		pos := anchor.GetToken().Position
		a.c.anchors[name] = append(a.c.anchors[name], anchorDef{line: pos.Line, column: pos.Column, value: anchor.Value})
	}
	return a
}

// lookupAnchor returns the value of the last definition of the anchor name before alias.
func (c *compiler) lookupAnchor(name string, alias ast.Node) (ast.Node, bool) {
	pos := alias.GetToken().Position
	var found ast.Node
	for _, def := range c.anchors[name] {
		if def.line < pos.Line || (def.line == pos.Line && def.column < pos.Column) {
			found = def.value
		}
	}
	return found, found != nil
}

// resolve strips anchors and follows aliases and tags.
// forceString is true when the value was tagged !!str.
func (c *compiler) resolve(n ast.Node, depth int) (node ast.Node, forceString bool) {
	for range maxDepth {
		switch v := n.(type) {
		case *ast.AnchorNode:
			n = v.Value
		case *ast.AliasNode:
			target, ok := c.lookupAnchor(v.Value.String(), v)
			if !ok {
				c.errorf(v, "unknown alias *%s", v.Value.String())
				return nil, false
			}
			n = target
		case *ast.TagNode:
			switch tag := v.Start.Value; tag {
			case "!!str":
				forceString = true
			case "!!int", "!!float", "!!bool", "!!null", "!!seq", "!!map":
			default:
				c.errorf(v, "unsupported YAML tag %s", tag)
				return nil, false
			}
			n = v.Value
		default:
			return n, forceString
		}
	}
	c.errorf(n, "too deeply nested")
	return nil, false
}

func describe(n ast.Node) string {
	switch n.(type) {
	case nil:
		return "nothing"
	case *ast.NullNode:
		return "null"
	case *ast.StringNode, *ast.LiteralNode:
		return "a string"
	case *ast.IntegerNode:
		return "an integer"
	case *ast.FloatNode, *ast.InfinityNode, *ast.NanNode:
		return "a float"
	case *ast.BoolNode:
		return "a bool"
	case *ast.SequenceNode:
		return "a sequence"
	case *ast.MappingNode, *ast.MappingValueNode:
		return "a mapping"
	default:
		return fmt.Sprintf("%T", n)
	}
}

// value compiles n into an expression of type t.
// When elide is true and the value is a composite literal, its type is omitted
// (the value is an element of an enclosing composite literal).
func (c *compiler) value(t types.Type, n ast.Node, elide bool, depth int) (string, bool) {
	if depth > maxDepth {
		c.errorf(n, "too deeply nested")
		return "", false
	}
	orig := n
	n, forceString := c.resolve(n, depth)
	if n == nil {
		return "", false
	}
	t = types.Unalias(t)
	_, isNull := n.(*ast.NullNode)

	switch {
	case isTime(t):
		return c.timeValue(n, forceString)
	case isDuration(t):
		return c.durationValue(t, n, forceString)
	case isText(t, c.enums):
		return c.textValue(t, n, forceString)
	case isBytes(t):
		if s, ok := stringValue(n, forceString); ok {
			return c.im.TypeString(t) + "(" + strconv.Quote(s) + ")", true
		}
	}

	switch u := t.Underlying().(type) {
	case *types.Basic:
		return c.basicValue(t, u, n, forceString)
	case *types.Pointer:
		if isNull {
			return "nil", true
		}
		elem := types.Unalias(u.Elem())
		if st, ok := elem.Underlying().(*types.Struct); ok && !isTime(elem) && !isText(elem, c.enums) {
			fields, ok := c.structFields(st, n, depth+1, false)
			if !ok {
				return "", false
			}
			lit := "{\n" + joinFields(fields) + "}"
			if elide {
				return lit, true
			}
			return "&" + c.im.TypeString(elem) + lit, true
		}
		v, ok := c.value(elem, orig, false, depth+1)
		if !ok {
			return "", false
		}
		c.usesPtr = true
		return fmt.Sprintf("%s[%s](%s)", c.ptrFunc, c.im.TypeString(elem), v), true
	case *types.Slice:
		if isNull {
			return "nil", true
		}
		elems, ok := c.sequence(u.Elem(), n, -1, depth)
		if !ok {
			return "", false
		}
		return c.composite(t, elide, elems), true
	case *types.Array:
		elems, ok := c.sequence(u.Elem(), n, int(u.Len()), depth)
		if !ok {
			return "", false
		}
		return c.composite(t, elide, elems), true
	case *types.Map:
		if isNull {
			return "nil", true
		}
		entries, ok := c.mapping(n, depth)
		if !ok {
			return "", false
		}
		var (
			elems []string
			seen  = map[string]ast.Node{}
			valid = true
		)
		for _, e := range entries {
			k, kok := c.value(u.Key(), e.keyNode, true, depth+1)
			v, vok := c.value(u.Elem(), e.value, true, depth+1)
			if !kok || !vok {
				valid = false
				continue
			}
			if _, dup := seen[k]; dup {
				c.errorf(e.keyNode, "duplicate map key %s", k)
				valid = false
				continue
			}
			seen[k] = e.keyNode
			elems = append(elems, k+": "+v)
		}
		if !valid {
			return "", false
		}
		return c.composite(t, elide, elems), true
	case *types.Struct:
		fields, ok := c.structFields(u, n, depth+1, false)
		if !ok {
			return "", false
		}
		lit := "{\n" + joinFields(fields) + "}"
		if elide {
			return lit, true
		}
		return c.im.TypeString(t) + lit, true
	default:
		c.errorf(n, "unsupported type %s", typeName(t))
		return "", false
	}
}

func (c *compiler) composite(t types.Type, elide bool, elems []string) string {
	var b strings.Builder
	if !elide {
		b.WriteString(c.im.TypeString(t))
	}
	b.WriteString("{")
	if inline := strings.Join(elems, ", "); len(inline) <= 60 && !strings.Contains(inline, "\n") {
		b.WriteString(inline)
	} else {
		b.WriteString("\n")
		for _, e := range elems {
			b.WriteString(e)
			b.WriteString(",\n")
		}
	}
	b.WriteString("}")
	return b.String()
}

// sequence compiles a YAML sequence; wantLen is the required length, or -1 for any.
func (c *compiler) sequence(elem types.Type, n ast.Node, wantLen int, depth int) ([]string, bool) {
	seq, ok := n.(*ast.SequenceNode)
	if !ok {
		c.errorf(n, "expected a sequence, got %s", describe(n))
		return nil, false
	}
	if wantLen >= 0 && len(seq.Values) != wantLen {
		c.errorf(n, "expected a sequence of exactly %d elements, got %d", wantLen, len(seq.Values))
		return nil, false
	}
	elems := make([]string, 0, len(seq.Values))
	valid := true
	for _, item := range seq.Values {
		v, ok := c.value(elem, item, true, depth+1)
		if !ok {
			valid = false
			continue
		}
		elems = append(elems, v)
	}
	return elems, valid
}

type mapEntry struct {
	key     string
	keyNode ast.Node
	value   ast.Node
}

// mapping returns the entries of a YAML mapping in source order, applying merge keys (<<).
// Keys given explicitly take precedence over merged ones; among merged mappings the first wins.
func (c *compiler) mapping(n ast.Node, depth int) ([]mapEntry, bool) {
	if depth > maxDepth {
		c.errorf(n, "too deeply nested")
		return nil, false
	}
	var values []*ast.MappingValueNode
	switch m := n.(type) {
	case *ast.MappingNode:
		values = m.Values
	case *ast.MappingValueNode:
		values = []*ast.MappingValueNode{m}
	default:
		c.errorf(n, "expected a mapping, got %s", describe(n))
		return nil, false
	}

	var (
		explicit []mapEntry
		merged   []mapEntry
		seen     = map[string]bool{}
		valid    = true
	)
	for _, mv := range values {
		if mv.Key.IsMergeKey() {
			src, _ := c.resolve(mv.Value, depth)
			var sources []ast.Node
			if seq, ok := src.(*ast.SequenceNode); ok {
				sources = seq.Values
			} else {
				sources = []ast.Node{mv.Value}
			}
			for _, s := range sources {
				s, _ = c.resolve(s, depth)
				if s == nil {
					valid = false
					continue
				}
				entries, ok := c.mapping(s, depth+1)
				if !ok {
					valid = false
					continue
				}
				merged = append(merged, entries...)
			}
			continue
		}
		keyNode, _ := c.resolve(mv.Key, depth)
		key, ok := scalarKey(keyNode)
		if !ok {
			c.errorf(mv.Key, "unsupported mapping key %s", describe(keyNode))
			valid = false
			continue
		}
		if seen[key] {
			c.errorf(mv.Key, "duplicate key %q", key)
			valid = false
			continue
		}
		seen[key] = true
		explicit = append(explicit, mapEntry{key: key, keyNode: mv.Key, value: mv.Value})
	}
	for _, e := range merged {
		if !seen[e.key] {
			seen[e.key] = true
			explicit = append(explicit, e)
		}
	}
	return explicit, valid
}

func scalarKey(n ast.Node) (string, bool) {
	switch k := n.(type) {
	case *ast.StringNode:
		return k.Value, true
	case *ast.IntegerNode, *ast.FloatNode, *ast.BoolNode:
		return k.GetToken().Value, true
	default:
		return "", false
	}
}

type fieldExpr struct {
	name string
	expr string
}

func joinFields(fs []fieldExpr) string {
	var b strings.Builder
	for _, f := range fs {
		b.WriteString(f.name)
		b.WriteString(": ")
		b.WriteString(f.expr)
		b.WriteString(",\n")
	}
	return b.String()
}

// structFields compiles a YAML mapping into the field initialisers of st.
// Unknown keys and missing required fields are reported as errors.
// keyLevel is true for records and the structs inlined into them, where logical keys live.
func (c *compiler) structFields(st *types.Struct, n ast.Node, depth int, keyLevel bool) ([]fieldExpr, bool) {
	n, _ = c.resolve(n, depth)
	if n == nil {
		return nil, false
	}
	entries, ok := c.mapping(n, depth)
	if !ok {
		return nil, false
	}
	remaining := map[string]*mapEntry{}
	for i := range entries {
		remaining[entries[i].key] = &entries[i]
	}
	fields, valid := c.consumeFields(st, n, remaining, depth, keyLevel)
	for _, e := range entries {
		if _, unknown := remaining[e.key]; unknown {
			c.errorf(e.keyNode, "unknown field %q", e.key)
			valid = false
		}
	}
	return fields, valid
}

// consumeFields fills the fields of st from remaining, deleting the entries it uses.
// Inline structs share remaining with their parent.
func (c *compiler) consumeFields(st *types.Struct, mappingNode ast.Node, remaining map[string]*mapEntry, depth int, keyLevel bool) ([]fieldExpr, bool) {
	var (
		fields []fieldExpr
		valid  = true
	)
	for _, f := range yamlFields(st) {
		if f.tag.inline {
			inner := types.Unalias(f.v.Type()).Underlying().(*types.Struct)
			sub, ok := c.consumeFields(inner, mappingNode, remaining, depth+1, keyLevel)
			if !ok {
				valid = false
			}
			if len(sub) > 0 {
				fields = append(fields, fieldExpr{
					name: f.v.Name(),
					expr: c.im.TypeString(f.v.Type()) + "{\n" + joinFields(sub) + "}",
				})
			}
			continue
		}
		isKey := keyLevel && c.keyVars[f.v]
		nonzero := f.constraints.nonzero || isKey
		e, present := remaining[f.yamlName]
		if !present {
			if f.required() || nonzero {
				c.errorf(mappingNode, "missing required field %q", f.yamlName)
				valid = false
			}
			continue
		}
		delete(remaining, f.yamlName)
		if nonzero && c.isZero(f.v.Type(), e.value) {
			c.errorf(e.value, "field %q must not be empty or zero", f.yamlName)
			valid = false
			continue
		}
		var captured []RefValue
		outer := c.capture
		if ref := c.refs[f.v]; ref != nil {
			c.capture = &captured
		}
		v, ok := c.value(f.v.Type(), e.value, false, depth+1)
		c.capture = outer
		if !ok {
			valid = false
			continue
		}
		if isKey {
			c.recordKeys[f.v] = v
		}
		for _, rv := range captured {
			rv.Ref = c.refs[f.v]
			c.recordRefs = append(c.recordRefs, rv)
		}
		fields = append(fields, fieldExpr{name: f.v.Name(), expr: v})
	}
	return fields, valid
}

func stringValue(n ast.Node, forceString bool) (string, bool) {
	switch v := n.(type) {
	case *ast.StringNode:
		return v.Value, true
	case *ast.LiteralNode:
		return v.Value.Value, true
	}
	if forceString {
		if _, ok := n.(ast.ScalarNode); ok {
			return n.GetToken().Value, true
		}
	}
	return "", false
}

// basicValue compiles a scalar, checking enum membership and capturing reference values.
func (c *compiler) basicValue(t types.Type, b *types.Basic, n ast.Node, forceString bool) (string, bool) {
	lit, ok := c.basicLiteral(t, b, n, forceString)
	if !ok {
		return "", false
	}
	if c.enums != nil {
		e, isEnum, err := c.enums.Lookup(t)
		if err != nil {
			c.errorf(n, "%v", err)
			return "", false
		}
		if isEnum && !e.Literals[lit] {
			c.errorf(n, "%s is not a valid %s (allowed: %s)", lit, typeName(t), strings.Join(e.Names, ", "))
			return "", false
		}
	}
	if c.capture != nil {
		*c.capture = append(*c.capture, RefValue{Lit: lit, Pos: c.pos(n)})
	}
	return lit, true
}

// textValue compiles a value decoded at runtime with encoding.TextUnmarshaler.
// The text is validated at generation time by runTextChecks.
func (c *compiler) textValue(t types.Type, n ast.Node, forceString bool) (string, bool) {
	s, ok := stringValue(n, forceString)
	if !ok {
		switch n.(type) {
		case *ast.IntegerNode, *ast.FloatNode, *ast.BoolNode:
			s, ok = n.GetToken().Value, true
		}
	}
	if !ok {
		c.errorf(n, "expected a scalar for %s, got %s", typeName(t), describe(n))
		return "", false
	}
	c.texts = append(c.texts, textCheck{typ: t, text: s, pos: c.pos(n)})
	c.usesText = true
	return fmt.Sprintf("%s[%s](%s)", c.textFunc, c.im.TypeString(t), strconv.Quote(s)), true
}

// isZero reports whether n denotes the zero value of t (or an empty slice or map).
// Malformed values are not zero; value reports them.
func (c *compiler) isZero(t types.Type, n ast.Node) bool {
	n, forceString := c.resolve(n, 0)
	if n == nil {
		return false
	}
	if _, ok := n.(*ast.NullNode); ok {
		return true
	}
	t = types.Unalias(t)
	switch {
	case isTime(t):
		s, _ := stringValue(n, forceString)
		for _, layout := range timeLayouts {
			if tm, err := time.Parse(layout, s); err == nil {
				return tm.IsZero()
			}
		}
		return false
	case isDuration(t):
		s, _ := stringValue(n, forceString)
		d, err := time.ParseDuration(s)
		return err == nil && d == 0
	case isBytes(t):
		if s, ok := stringValue(n, forceString); ok {
			return s == ""
		}
	}
	switch u := t.Underlying().(type) {
	case *types.Basic:
		switch v := n.(type) {
		case *ast.StringNode, *ast.LiteralNode:
			s, _ := stringValue(n, forceString)
			return u.Info()&types.IsString != 0 && s == ""
		case *ast.IntegerNode:
			return !forceString && (v.Value == int64(0) || v.Value == uint64(0))
		case *ast.FloatNode:
			return !forceString && v.Value == 0
		case *ast.BoolNode:
			return !forceString && !v.Value
		}
	case *types.Slice, *types.Array:
		seq, ok := n.(*ast.SequenceNode)
		return ok && len(seq.Values) == 0
	case *types.Map:
		m, ok := n.(*ast.MappingNode)
		return ok && len(m.Values) == 0
	}
	return false
}

func (c *compiler) basicLiteral(t types.Type, b *types.Basic, n ast.Node, forceString bool) (string, bool) {
	info := b.Info()
	switch {
	case info&types.IsString != 0:
		s, ok := stringValue(n, forceString)
		if !ok {
			c.errorf(n, "expected a string for type %s, got %s (quote the value to use it as a string)", typeName(t), describe(n))
			return "", false
		}
		return strconv.Quote(s), true

	case info&types.IsBoolean != 0:
		v, ok := n.(*ast.BoolNode)
		if !ok || forceString {
			c.errorf(n, "expected a bool for type %s, got %s", typeName(t), describe(n))
			return "", false
		}
		return strconv.FormatBool(v.Value), true

	case info&types.IsInteger != 0:
		v, ok := n.(*ast.IntegerNode)
		if !ok || forceString {
			hint := ""
			if e, isEnum, _ := c.enums.Lookup(t); isEnum {
				// Integer enums are written with their numeric values, even when the type
				// has a text form (UnmarshalText), because the literal must be known here.
				hint = " (integer enums are written with their numeric value: " + strings.Join(e.Pairs, ", ") + ")"
			}
			c.errorf(n, "expected an integer for type %s, got %s%s", typeName(t), describe(n), hint)
			return "", false
		}
		return c.integer(t, b, v)

	case info&types.IsFloat != 0:
		var f float64
		switch v := n.(type) {
		case *ast.FloatNode:
			f = v.Value
		case *ast.IntegerNode:
			switch iv := v.Value.(type) {
			case int64:
				f = float64(iv)
			case uint64:
				f = float64(iv)
			}
		case *ast.StringNode:
			// The YAML 1.2 core schema resolves plain scalars such as 1e10 to floats,
			// but the parser keeps them as strings.
			parsed, err := strconv.ParseFloat(v.Value, 64)
			if err != nil || v.GetToken().Type != token.StringType || forceString {
				c.errorf(n, "expected a number for type %s, got a string", typeName(t))
				return "", false
			}
			f = parsed
		case *ast.InfinityNode, *ast.NanNode:
			m := c.im.Add("math", "math")
			expr := m + ".NaN()"
			if inf, ok := v.(*ast.InfinityNode); ok {
				expr = fmt.Sprintf("%s.Inf(%d)", m, int(math.Copysign(1, inf.Value)))
			}
			if !types.Identical(t, types.Typ[types.Float64]) {
				expr = c.im.TypeString(t) + "(" + expr + ")"
			}
			return expr, true
		default:
			c.errorf(n, "expected a number for type %s, got %s", typeName(t), describe(n))
			return "", false
		}
		if forceString {
			c.errorf(n, "expected a number for type %s, got a string", typeName(t))
			return "", false
		}
		if b.Kind() == types.Float32 && math.Abs(f) > math.MaxFloat32 {
			c.errorf(n, "%g overflows %s", f, typeName(t))
			return "", false
		}
		return strconv.FormatFloat(f, 'g', -1, 64), true
	}
	c.errorf(n, "unsupported type %s", typeName(t))
	return "", false
}

func (c *compiler) integer(t types.Type, b *types.Basic, n *ast.IntegerNode) (string, bool) {
	bits := 64
	switch b.Kind() {
	case types.Int8, types.Uint8:
		bits = 8
	case types.Int16, types.Uint16:
		bits = 16
	case types.Int32, types.Uint32:
		bits = 32
	}
	unsigned := b.Info()&types.IsUnsigned != 0
	switch v := n.Value.(type) {
	case int64:
		if unsigned {
			if v < 0 || (bits < 64 && uint64(v) > (1<<bits)-1) {
				c.errorf(n, "%d overflows %s", v, typeName(t))
				return "", false
			}
		} else if bits < 64 && (v < -(1<<(bits-1)) || v > (1<<(bits-1))-1) {
			c.errorf(n, "%d overflows %s", v, typeName(t))
			return "", false
		}
		return strconv.FormatInt(v, 10), true
	case uint64:
		limit := uint64(1)<<(bits-1) - 1
		if unsigned {
			limit = ^uint64(0) >> (64 - bits)
		}
		if v > limit {
			c.errorf(n, "%d overflows %s", v, typeName(t))
			return "", false
		}
		return strconv.FormatUint(v, 10), true
	}
	c.errorf(n, "invalid integer %s", n.GetToken().Value)
	return "", false
}

var timeLayouts = []string{
	time.RFC3339Nano,
	"2006-01-02T15:04:05.999999999",
	"2006-01-02 15:04:05.999999999Z07:00",
	"2006-01-02 15:04:05.999999999 -07:00",
	"2006-01-02 15:04:05.999999999",
	time.DateOnly,
}

// timeValue compiles a timestamp into a time.Date call.
// Timestamps without a zone are interpreted as UTC.
func (c *compiler) timeValue(n ast.Node, forceString bool) (string, bool) {
	s, ok := stringValue(n, forceString)
	if !ok {
		c.errorf(n, "expected a timestamp string for time.Time, got %s", describe(n))
		return "", false
	}
	var (
		tm  time.Time
		err error
	)
	for _, layout := range timeLayouts {
		if tm, err = time.Parse(layout, s); err == nil {
			break
		}
	}
	if err != nil {
		c.errorf(n, "invalid timestamp %q: use RFC 3339 (e.g. 2006-01-02T15:04:05Z07:00) or a date (2006-01-02)", s)
		return "", false
	}
	pkg := c.im.Add("time", "time")
	_, offset := tm.Zone()
	loc := pkg + ".UTC"
	if offset != 0 {
		loc = fmt.Sprintf("%s.FixedZone(%q, %d)", pkg, tm.Format("-07:00"), offset)
	}
	return fmt.Sprintf("%s.Date(%d, %s.%s, %d, %d, %d, %d, %d, %s)",
		pkg, tm.Year(), pkg, tm.Month(), tm.Day(), tm.Hour(), tm.Minute(), tm.Second(), tm.Nanosecond(), loc), true
}

// durationValue compiles a duration string such as "1h30m" into a constant expression.
func (c *compiler) durationValue(t types.Type, n ast.Node, forceString bool) (string, bool) {
	s, ok := stringValue(n, forceString)
	if !ok {
		c.errorf(n, "expected a duration string such as \"1h30m\" for %s, got %s", typeName(t), describe(n))
		return "", false
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		c.errorf(n, "invalid duration %q: %v", s, err)
		return "", false
	}
	if d == 0 {
		return "0", true
	}
	pkg := c.im.Add("time", "time")
	for _, u := range []struct {
		d    time.Duration
		name string
	}{
		{time.Hour, "Hour"}, {time.Minute, "Minute"}, {time.Second, "Second"},
		{time.Millisecond, "Millisecond"}, {time.Microsecond, "Microsecond"},
	} {
		if d%u.d == 0 {
			return fmt.Sprintf("%d * %s.%s", d/u.d, pkg, u.name), true
		}
	}
	return fmt.Sprintf("%s.Duration(%d)", pkg, int64(d)), true
}

// typeName renders t for error messages, qualifying names by package name (e.g. "model.Color").
func typeName(t types.Type) string {
	return types.TypeString(t, func(p *types.Package) string { return p.Name() })
}

func (c *compiler) err() error {
	return errors.Join(c.errs...)
}
