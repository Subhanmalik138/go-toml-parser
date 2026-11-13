package toml

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Document is a parsed TOML document. Values are decoded as:
//
//	string        -> string
//	integer       -> int64
//	float         -> float64 (inf/nan included)
//	boolean       -> bool
//	datetime      -> time.Time (offset datetimes keep their location,
//	                 local datetimes/dates are UTC, local times use year 0)
//	array         -> []any
//	table         -> map[string]any
type Document struct {
	root map[string]any
}

// ParseFile reads and parses a TOML file.
func ParseFile(path string) (*Document, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("toml: %w", err)
	}
	return Parse(data)
}

// Root returns the document as a plain map. The caller must not mutate it.
func (d *Document) Root() map[string]any { return d.root }

// Keys returns the top-level keys in sorted order.
func (d *Document) Keys() []string {
	keys := make([]string, 0, len(d.root))
	for k := range d.root {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Get returns the value at a dotted path, e.g. "server.tls.enabled".
// Numeric segments index into arrays, so "ports.1" fetches the second
// element of the "ports" array. The second return value is false when any
// segment is missing or has the wrong type.
func (d *Document) Get(path string) (any, bool) {
	if path == "" {
		return nil, false
	}
	cur := any(d.root)
	for _, part := range strings.Split(path, ".") {
		switch node := cur.(type) {
		case map[string]any:
			v, ok := node[part]
			if !ok {
				return nil, false
			}
			cur = v
		case []any:
			i, err := strconv.Atoi(part)
			if err != nil || i < 0 || i >= len(node) {
				return nil, false
			}
			cur = node[i]
		default:
			return nil, false
		}
	}
	return cur, true
}

// ToJSON renders the document as indented JSON. Datetimes are rendered in
// RFC 3339 form; a local time (no date) renders with year 0000.
func (d *Document) ToJSON() ([]byte, error) {
	return json.MarshalIndent(d.root, "", "  ")
}

// FormatValue renders a single value the way the CLI prints it: strings
// raw, everything else as compact JSON.
func FormatValue(v any) string {
	switch val := v.(type) {
	case string:
		return val
	case time.Time:
		return val.Format(time.RFC3339Nano)
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprintf("%v", v)
		}
		return string(b)
	}
}
