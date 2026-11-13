// Package toml implements a zero-dependency TOML v1.0 parser.
//
// The parser is a hand-written recursive-descent parser over the raw input:
// there is no separate lexer, because TOML's value grammar (datetimes,
// numbers, multi-line strings) is easier to classify in context. It is
// strict where TOML is strict: duplicate keys, redefined tables, mixed-type
// arrays and extending an inline table are all errors, each reported with
// the line and column where the problem starts.
package toml

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// ParseError is a TOML syntax error with position information.
type ParseError struct {
	Line    int
	Column  int
	Message string
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("toml: line %d, column %d: %s", e.Line, e.Column, e.Message)
}

// parser holds the state of a single parse run.
type parser struct {
	src  string // full input
	pos  int    // byte offset of the next rune
	line int    // 1-based
	col  int    // 1-based, in runes
}

// tableKind records how a table at a given dotted path came into being, so
// the redefinition rules can be enforced.
type tableKind string

const (
	kindImplicit tableKind = "implicit" // created as a parent of a [header]
	kindDotted   tableKind = "dotted"   // created by a dotted key (a.b = 1)
	kindExplicit tableKind = "explicit" // created by its own [header]
	kindInline   tableKind = "inline"   // created as an inline table { ... }
	kindArray    tableKind = "array"    // created by a [[header]]
)

type parseState struct {
	p      *parser
	kinds  map[string]tableKind // dotted path -> how it was created
	arrays map[string]bool      // dotted paths that are arrays of tables
}

func joinPath(parts []string) string { return strings.Join(parts, "\x00") }

// Parse parses TOML data and returns the document.
func Parse(data []byte) (*Document, error) {
	return ParseString(string(data))
}

// ParseString parses a TOML document from a string.
func ParseString(src string) (*Document, error) {
	st := &parseState{
		p:      &parser{src: src, line: 1, col: 1},
		kinds:  map[string]tableKind{},
		arrays: map[string]bool{},
	}
	doc := &Document{root: map[string]any{}}
	current := doc.root
	currentPath := []string{}

	for {
		st.p.skipWhitespaceAndNewlines()
		if st.p.eof() {
			break
		}
		if st.p.peek() == '[' {
			var err error
			current, currentPath, err = st.parseHeader(doc.root, currentPath)
			if err != nil {
				return nil, err
			}
		} else {
			if err := st.parseKeyValue(current, currentPath); err != nil {
				return nil, err
			}
		}
	}
	return doc, nil
}

// --- low-level cursor helpers ---

func (p *parser) eof() bool { return p.pos >= len(p.src) }

func (p *parser) peek() byte {
	if p.eof() {
		return 0
	}
	return p.src[p.pos]
}

func (p *parser) peekAt(n int) byte {
	if p.pos+n >= len(p.src) {
		return 0
	}
	return p.src[p.pos+n]
}

// next consumes one rune and advances line/column tracking.
func (p *parser) next() rune {
	if p.eof() {
		return utf8.RuneError
	}
	r, size := utf8.DecodeRuneInString(p.src[p.pos:])
	p.pos += size
	if r == '\n' {
		p.line++
		p.col = 1
	} else {
		p.col++
	}
	return r
}

func (p *parser) err(format string, args ...any) error {
	return &ParseError{Line: p.line, Column: p.col, Message: fmt.Sprintf(format, args...)}
}

func (p *parser) errAt(line, col int, format string, args ...any) error {
	return &ParseError{Line: line, Column: col, Message: fmt.Sprintf(format, args...)}
}

// skipInlineWhitespace consumes spaces and tabs, but not newlines.
func (p *parser) skipInlineWhitespace() {
	for !p.eof() && (p.peek() == ' ' || p.peek() == '\t') {
		p.next()
	}
}

// skipComment consumes a '#...' comment up to (not including) the newline.
func (p *parser) skipComment() {
	if p.peek() == '#' {
		for !p.eof() && p.peek() != '\n' {
			p.next()
		}
	}
}

// skipWhitespaceAndNewlines consumes spaces, tabs, newlines and comments.
func (p *parser) skipWhitespaceAndNewlines() {
	for !p.eof() {
		switch p.peek() {
		case ' ', '\t', '\n', '\r':
			p.next()
		case '#':
			p.skipComment()
		default:
			return
		}
	}
}

// expect consumes c or returns an error.
func (p *parser) expect(c byte, what string) error {
	if p.eof() || p.peek() != c {
		return p.err("expected %s, got %q", what, p.describe())
	}
	p.next()
	return nil
}

func (p *parser) describe() string {
	if p.eof() {
		return "end of input"
	}
	r, _ := utf8.DecodeRuneInString(p.src[p.pos:])
	if r == '\n' {
		return "newline"
	}
	return string(r)
}

// --- headers ---

// parseHeader handles [table] and [[array-of-tables]] headers. It returns
// the new current table and its path.
func (st *parseState) parseHeader(root map[string]any, _ []string) (map[string]any, []string, error) {
	p := st.p
	startLine, startCol := p.line, p.col
	p.next() // consume '['
	isArray := false
	if p.peek() == '[' {
		isArray = true
		p.next()
	}
	p.skipInlineWhitespace()
	key, err := st.parseKey()
	if err != nil {
		return nil, nil, err
	}
	p.skipInlineWhitespace()
	if err := p.expect(']', "']'"); err != nil {
		return nil, nil, err
	}
	if isArray {
		if err := p.expect(']', "']]'"); err != nil {
			return nil, nil, err
		}
	}
	// Only whitespace/comment/newline may follow a header on the line.
	p.skipInlineWhitespace()
	p.skipComment()
	if !p.eof() && p.peek() != '\n' && p.peek() != '\r' {
		return nil, nil, p.err("unexpected %q after table header", p.describe())
	}

	if isArray {
		return st.openArrayTable(root, key, startLine, startCol)
	}
	return st.openTable(root, key, startLine, startCol)
}

// navigate walks parts from root, creating implicit intermediate tables.
// It returns the parent map of the last part and the last part itself.
func (st *parseState) navigate(root map[string]any, parts []string) (map[string]any, string, error) {
	cur := root
	curPath := []string{}
	for _, part := range parts[:len(parts)-1] {
		curPath = append(curPath, part)
		next, ok := cur[part]
		if !ok {
			child := map[string]any{}
			cur[part] = child
			st.kinds[joinPath(curPath)] = kindImplicit
			cur = child
			continue
		}
		switch node := next.(type) {
		case map[string]any:
			cur = node
		case []any:
			// An array of tables: descend into its most recent element.
			if len(node) == 0 {
				return nil, "", st.p.err("empty array of tables at %q", strings.Join(curPath, "."))
			}
			last, ok := node[len(node)-1].(map[string]any)
			if !ok {
				return nil, "", st.p.err("key %q is not a table", strings.Join(curPath, "."))
			}
			cur = last
		default:
			return nil, "", st.p.err("key %q is already defined as a value", strings.Join(curPath, "."))
		}
	}
	return cur, parts[len(parts)-1], nil
}

func (st *parseState) openTable(root map[string]any, key []string, line, col int) (map[string]any, []string, error) {
	parent, last, err := st.navigate(root, key)
	if err != nil {
		return nil, nil, err
	}
	full := joinPath(key)
	existing, ok := parent[last]
	if !ok {
		child := map[string]any{}
		parent[last] = child
		st.kinds[full] = kindExplicit
		return child, key, nil
	}
	// Reject redefinition. An implicitly created parent may be claimed once;
	// anything else (explicit header, dotted key, inline table, array) may not.
	switch node := existing.(type) {
	case map[string]any:
		switch st.kinds[full] {
		case kindImplicit:
			st.kinds[full] = kindExplicit
			return node, key, nil
		default:
			return nil, nil, st.p.errAt(line, col, "table %q is already defined", strings.Join(key, "."))
		}
	default:
		return nil, nil, st.p.errAt(line, col, "key %q is already defined as a value", strings.Join(key, "."))
	}
}

func (st *parseState) openArrayTable(root map[string]any, key []string, line, col int) (map[string]any, []string, error) {
	parent, last, err := st.navigate(root, key)
	if err != nil {
		return nil, nil, err
	}
	full := joinPath(key)
	existing, ok := parent[last]
	child := map[string]any{}
	if !ok {
		parent[last] = []any{child}
		st.arrays[full] = true
		st.kinds[full] = kindArray
		return child, key, nil
	}
	switch node := existing.(type) {
	case []any:
		if !st.arrays[full] {
			return nil, nil, st.p.errAt(line, col, "key %q is an array, not an array of tables", strings.Join(key, "."))
		}
		parent[last] = append(node, child)
		return child, key, nil
	default:
		return nil, nil, st.p.errAt(line, col, "key %q is already defined", strings.Join(key, "."))
	}
}

// --- key/value pairs ---

func (st *parseState) parseKeyValue(current map[string]any, currentPath []string) error {
	p := st.p
	key, err := st.parseKey()
	if err != nil {
		return err
	}
	p.skipInlineWhitespace()
	if err := p.expect('=', "'='"); err != nil {
		return err
	}
	p.skipInlineWhitespace()
	value, err := st.parseValue(currentPath, key)
	if err != nil {
		return err
	}
	if err := st.setDotted(current, currentPath, key, value); err != nil {
		return err
	}
	// Only whitespace/comment/newline may follow a value on the line.
	p.skipInlineWhitespace()
	p.skipComment()
	if !p.eof() && p.peek() != '\n' && p.peek() != '\r' {
		return p.err("unexpected %q after value", p.describe())
	}
	return nil
}

// parseKey parses a (possibly dotted) key: bare, "basic" or 'literal' parts.
func (st *parseState) parseKey() ([]string, error) {
	p := st.p
	var parts []string
	for {
		p.skipInlineWhitespace()
		var part string
		var err error
		switch p.peek() {
		case '"':
			part, err = st.parseBasicString(false)
		case '\'':
			part, err = st.parseLiteralString(false)
		default:
			part, err = st.parseBareKey()
		}
		if err != nil {
			return nil, err
		}
		parts = append(parts, part)
		p.skipInlineWhitespace()
		if p.peek() != '.' {
			return parts, nil
		}
		p.next() // consume '.'
	}
}

func (st *parseState) parseBareKey() (string, error) {
	p := st.p
	start := p.pos
	for !p.eof() {
		c := p.peek()
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_' || c == '-' {
			p.next()
		} else {
			break
		}
	}
	if p.pos == start {
		return "", p.err("expected a key, got %q", p.describe())
	}
	return p.src[start:p.pos], nil
}

// setDotted assigns value under key (a dotted path) inside table, creating
// intermediate tables. currentPath is the path of the table itself, used
// only to build full paths for the redefinition bookkeeping.
func (st *parseState) setDotted(table map[string]any, currentPath, key []string, value any) error {
	p := st.p
	cur := table
	full := append(append([]string{}, currentPath...), key...)
	for i, part := range key[:len(key)-1] {
		partPath := joinPath(full[:len(currentPath)+i+1])
		existing, ok := cur[part]
		if !ok {
			child := map[string]any{}
			cur[part] = child
			st.kinds[partPath] = kindDotted
			cur = child
			continue
		}
		node, isMap := existing.(map[string]any)
		if !isMap {
			return p.err("key %q is already defined as a value", strings.Join(full[:len(currentPath)+i+1], "."))
		}
		switch st.kinds[partPath] {
		case kindExplicit, kindInline:
			return p.err("cannot extend table %q with a dotted key: it was already defined", strings.Join(full[:len(currentPath)+i+1], "."))
		case kindArray:
			return p.err("cannot use a dotted key through array of tables %q", strings.Join(full[:len(currentPath)+i+1], "."))
		}
		cur = node
	}
	last := key[len(key)-1]
	lastPath := joinPath(full)
	if _, ok := cur[last]; ok {
		return p.err("duplicate key %q", strings.Join(full, "."))
	}
	if k, ok := st.kinds[lastPath]; ok && (k == kindExplicit || k == kindDotted || k == kindInline || k == kindArray) {
		return p.err("key %q is already defined as a table", strings.Join(full, "."))
	}
	cur[last] = value
	if _, isMap := value.(map[string]any); isMap {
		st.kinds[lastPath] = kindInline
	}
	return nil
}

// --- values ---

var (
	intDecRe = regexp.MustCompile(`^[+-]?(0|[1-9](_?\d)*)$`)
	intHexRe = regexp.MustCompile(`^0x[0-9A-Fa-f](_?[0-9A-Fa-f])*$`)
	intOctRe = regexp.MustCompile(`^0o[0-7](_?[0-7])*$`)
	intBinRe = regexp.MustCompile(`^0b[01](_?[01])*$`)
	// A float needs a '.' or an exponent (or be inf/nan); plain digits are ints.
	floatRe    = regexp.MustCompile(`^[+-]?(\d(_?\d)*)?\.\d(_?\d)*([eE][+-]?\d(_?\d)*)?$|^[+-]?\d(_?\d)*[eE][+-]?\d(_?\d)*$`)
	infNanRe   = regexp.MustCompile(`^[+-]?(inf|nan)$`)
	dateRe     = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}([Tt ]\d{2}:\d{2}:\d{2}(\.\d+)?([Zz]|[+-]\d{2}:\d{2})?)?$`)
	dateOnlyRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	timeRe     = regexp.MustCompile(`^\d{2}:\d{2}:\d{2}(\.\d+)?$`)
)

func (st *parseState) parseValue(currentPath, key []string) (any, error) {
	p := st.p
	switch {
	case p.peek() == '"':
		if p.peekAt(1) == '"' && p.peekAt(2) == '"' {
			return st.parseBasicString(true)
		}
		return st.parseBasicString(false)
	case p.peek() == '\'':
		if p.peekAt(1) == '\'' && p.peekAt(2) == '\'' {
			return st.parseLiteralString(true)
		}
		return st.parseLiteralString(false)
	case p.peek() == '[':
		return st.parseArray()
	case p.peek() == '{':
		return st.parseInlineTable(currentPath, key)
	default:
		return st.parseBareValue()
	}
}

// parseBareValue reads an unquoted value (number, boolean, datetime) up to
// the next delimiter and classifies it.
func (st *parseState) parseBareValue() (any, error) {
	p := st.p
	startLine, startCol := p.line, p.col
	start := p.pos
	for !p.eof() {
		c := p.peek()
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == ',' || c == ']' || c == '}' || c == '#' {
			break
		}
		p.next()
	}
	token := p.src[start:p.pos]
	if token == "" {
		return nil, p.err("expected a value, got %q", p.describe())
	}
	// A space-separated datetime ("1979-05-27 07:32:00") contains a space,
	// so rejoin a bare date with the time that follows it.
	if dateOnlyRe.MatchString(token) && p.peek() == ' ' &&
		isDigit(p.peekAt(1)) && isDigit(p.peekAt(2)) && p.peekAt(3) == ':' {
		p.next() // consume the space
		token += " "
		start = p.pos
		for !p.eof() {
			c := p.peek()
			if c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == ',' || c == ']' || c == '}' || c == '#' {
				break
			}
			p.next()
		}
		token += p.src[start:p.pos]
	}
	switch token {
	case "true":
		return true, nil
	case "false":
		return false, nil
	}
	if dateRe.MatchString(token) || timeRe.MatchString(token) {
		if t, ok := parseDatetime(token); ok {
			return t, nil
		}
		return nil, p.errAt(startLine, startCol, "invalid datetime %q", token)
	}
	if intDecRe.MatchString(token) || intHexRe.MatchString(token) || intOctRe.MatchString(token) || intBinRe.MatchString(token) {
		clean := strings.ReplaceAll(token, "_", "")
		v, err := strconv.ParseInt(clean, 0, 64)
		if err != nil {
			return nil, p.errAt(startLine, startCol, "integer %q out of range", token)
		}
		return v, nil
	}
	if floatRe.MatchString(token) || infNanRe.MatchString(token) {
		return parseFloat(token, startLine, startCol)
	}
	return nil, p.errAt(startLine, startCol, "invalid value %q", token)
}

func parseFloat(token string, line, col int) (any, error) {
	switch strings.ToLower(strings.TrimLeft(token, "+-")) {
	case "inf":
		if strings.HasPrefix(token, "-") {
			return math.Inf(-1), nil
		}
		return math.Inf(1), nil
	case "nan":
		return math.NaN(), nil
	}
	clean := strings.ReplaceAll(token, "_", "")
	v, err := strconv.ParseFloat(clean, 64)
	if err != nil {
		return nil, &ParseError{Line: line, Column: col, Message: fmt.Sprintf("invalid float %q", token)}
	}
	return v, nil
}

// parseDatetime tries each TOML datetime shape. Offset datetimes keep their
// location; local datetimes/dates are UTC; local times use year 0.
func parseDatetime(s string) (time.Time, bool) {
	norm := s
	// Normalize the separator and the T/Z markers to upper case.
	if len(norm) > 10 && (norm[10] == ' ' || norm[10] == 't') {
		norm = norm[:10] + "T" + norm[11:]
	}
	if strings.HasSuffix(norm, "z") {
		norm = norm[:len(norm)-1] + "Z"
	}
	layouts := []string{
		time.RFC3339Nano,
		"2006-01-02T15:04:05.999999999",
		"2006-01-02",
		"15:04:05.999999999",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, norm); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// parseArray parses [v1, v2, ...], spanning lines, with a trailing comma
// allowed. Arrays must hold a single type (TOML v1.0 rule).
func (st *parseState) parseArray() (any, error) {
	p := st.p
	line, col := p.line, p.col
	p.next() // consume '['
	var items []any
	var elemType string
	for {
		p.skipWhitespaceAndNewlines()
		if p.eof() {
			return nil, p.err("unterminated array")
		}
		if p.peek() == ']' {
			p.next()
			return items, nil
		}
		v, err := st.parseValue(nil, nil)
		if err != nil {
			return nil, err
		}
		t := valueTypeName(v)
		if elemType == "" {
			elemType = t
		} else if t != elemType {
			return nil, p.errAt(line, col, "mixed-type array: %s and %s", elemType, t)
		}
		items = append(items, v)
		p.skipWhitespaceAndNewlines()
		if p.eof() {
			return nil, p.err("unterminated array")
		}
		switch p.peek() {
		case ',':
			p.next()
		case ']':
			p.next()
			return items, nil
		default:
			return nil, p.err("expected ',' or ']' in array, got %q", p.describe())
		}
	}
}

func valueTypeName(v any) string {
	switch v.(type) {
	case string:
		return "string"
	case int64:
		return "integer"
	case float64:
		return "float"
	case bool:
		return "boolean"
	case time.Time:
		return "datetime"
	case []any:
		return "array"
	case map[string]any:
		return "table"
	default:
		return "value"
	}
}

// parseInlineTable parses { k = v, ... }. Newlines are not allowed inside
// (TOML v1.0), and the table is closed once parsed.
func (st *parseState) parseInlineTable(currentPath, key []string) (any, error) {
	p := st.p
	p.next() // consume '{'
	table := map[string]any{}
	full := append(append([]string{}, currentPath...), key...)
	for {
		p.skipInlineWhitespace()
		if p.eof() {
			return nil, p.err("unterminated inline table")
		}
		if p.peek() == '}' {
			p.next()
			return table, nil
		}
		if p.peek() == '\n' {
			return nil, p.err("newline is not allowed in an inline table")
		}
		k, err := st.parseKey()
		if err != nil {
			return nil, err
		}
		p.skipInlineWhitespace()
		if err := p.expect('=', "'='"); err != nil {
			return nil, err
		}
		p.skipInlineWhitespace()
		v, err := st.parseValue(full, k)
		if err != nil {
			return nil, err
		}
		if err := st.setDotted(table, full, k, v); err != nil {
			return nil, err
		}
		p.skipInlineWhitespace()
		if p.eof() {
			return nil, p.err("unterminated inline table")
		}
		switch p.peek() {
		case ',':
			p.next()
		case '}':
			p.next()
			return table, nil
		default:
			return nil, p.err("expected ',' or '}' in inline table, got %q", p.describe())
		}
	}
}

// --- strings ---

// parseBasicString parses "..." or """...""" (multiline), processing escapes.
func (st *parseState) parseBasicString(multiline bool) (string, error) {
	p := st.p
	if multiline {
		p.next()
		p.next()
		p.next()
		// A newline immediately after the opening delimiter is trimmed.
		if p.peek() == '\n' {
			p.next()
		} else if p.peek() == '\r' && p.peekAt(1) == '\n' {
			p.next()
			p.next()
		}
	} else {
		p.next() // consume opening '"'
	}
	var sb strings.Builder
	for {
		if p.eof() {
			return "", p.err("unterminated string")
		}
		c := p.peek()
		if !multiline && c == '\n' {
			return "", p.err("newline in a single-line string")
		}
		if c == '"' {
			if multiline {
				// Count the run of quotes: the last three close the
				// string, any earlier ones are content.
				run := 0
				for p.peekAt(run) == '"' {
					run++
				}
				if run >= 3 {
					for i := 0; i < run-3; i++ {
						sb.WriteByte('"')
						p.next()
					}
					p.next()
					p.next()
					p.next()
					return sb.String(), nil
				}
				sb.WriteByte('"')
				p.next()
				continue
			}
			p.next()
			return sb.String(), nil
		}
		if c == '\\' {
			if err := st.parseEscape(&sb, multiline); err != nil {
				return "", err
			}
			continue
		}
		sb.WriteRune(p.next())
	}
}

// parseEscape handles one escape sequence (the backslash is current).
// In multiline strings a line-ending backslash trims the newline and all
// following whitespace.
func (st *parseState) parseEscape(sb *strings.Builder, multiline bool) error {
	p := st.p
	p.next() // consume '\'
	if p.eof() {
		return p.err("unterminated escape sequence")
	}
	if multiline {
		// Line-ending backslash: optional spaces/tabs, then a newline,
		// then all following whitespace (including newlines) is skipped.
		savePos, saveLine, saveCol := p.pos, p.line, p.col
		for p.peek() == ' ' || p.peek() == '\t' {
			p.next()
		}
		if p.peek() == '\n' || (p.peek() == '\r' && p.peekAt(1) == '\n') {
			if p.peek() == '\r' {
				p.next()
			}
			p.next()
			for !p.eof() && (p.peek() == ' ' || p.peek() == '\t' || p.peek() == '\n' || p.peek() == '\r') {
				p.next()
			}
			return nil
		}
		p.pos, p.line, p.col = savePos, saveLine, saveCol
	}
	esc := p.next()
	switch esc {
	case 'b':
		sb.WriteByte('\b')
	case 't':
		sb.WriteByte('\t')
	case 'n':
		sb.WriteByte('\n')
	case 'f':
		sb.WriteByte('\f')
	case 'r':
		sb.WriteByte('\r')
	case '"':
		sb.WriteByte('"')
	case '\\':
		sb.WriteByte('\\')
	case 'u', 'U':
		n := 4
		if esc == 'U' {
			n = 8
		}
		var code int64
		for i := 0; i < n; i++ {
			if p.eof() {
				return p.err("truncated unicode escape")
			}
			d := p.next()
			if !(d >= '0' && d <= '9' || d >= 'a' && d <= 'f' || d >= 'A' && d <= 'F') {
				return p.err("invalid unicode escape")
			}
			code = code*16 + int64(hexVal(d))
		}
		if code > 0x10FFFF || (code >= 0xD800 && code <= 0xDFFF) {
			return p.err("unicode escape out of range")
		}
		sb.WriteRune(rune(code))
	default:
		return p.err("invalid escape sequence \\%c", esc)
	}
	return nil
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }

func hexVal(r rune) int {
	switch {
	case r >= '0' && r <= '9':
		return int(r - '0')
	case r >= 'a' && r <= 'f':
		return int(r-'a') + 10
	default:
		return int(r-'A') + 10
	}
}

// parseLiteralString parses '...' or ”'...”' (multiline). No escapes.
func (st *parseState) parseLiteralString(multiline bool) (string, error) {
	p := st.p
	if multiline {
		p.next()
		p.next()
		p.next()
		if p.peek() == '\n' {
			p.next()
		} else if p.peek() == '\r' && p.peekAt(1) == '\n' {
			p.next()
			p.next()
		}
	} else {
		p.next() // consume opening '\''
	}
	var sb strings.Builder
	for {
		if p.eof() {
			return "", p.err("unterminated string")
		}
		c := p.peek()
		if !multiline && c == '\n' {
			return "", p.err("newline in a single-line string")
		}
		if c == '\'' {
			if multiline {
				run := 0
				for p.peekAt(run) == '\'' {
					run++
				}
				if run >= 3 {
					for i := 0; i < run-3; i++ {
						sb.WriteByte('\'')
						p.next()
					}
					p.next()
					p.next()
					p.next()
					return sb.String(), nil
				}
				sb.WriteByte('\'')
				p.next()
				continue
			}
			p.next()
			return sb.String(), nil
		}
		sb.WriteRune(p.next())
	}
}
