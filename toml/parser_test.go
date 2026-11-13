package toml

import (
	"math"
	"strings"
	"testing"
	"time"
)

func mustParse(t *testing.T, src string) *Document {
	t.Helper()
	doc, err := ParseString(src)
	if err != nil {
		t.Fatalf("ParseString(%q) failed: %v", src, err)
	}
	return doc
}

func mustGet(t *testing.T, doc *Document, path string) any {
	t.Helper()
	v, ok := doc.Get(path)
	if !ok {
		t.Fatalf("Get(%q) not found", path)
	}
	return v
}

func TestScalars(t *testing.T) {
	doc := mustParse(t, `
title = "TOML Example"
literal = 'C:\Users\nodejs\templates'
enabled = true
disabled = false
`)
	if got := mustGet(t, doc, "title"); got != "TOML Example" {
		t.Errorf("title = %v, want TOML Example", got)
	}
	if got := mustGet(t, doc, "literal"); got != `C:\Users\nodejs\templates` {
		t.Errorf("literal = %v", got)
	}
	if got := mustGet(t, doc, "enabled"); got != true {
		t.Errorf("enabled = %v", got)
	}
	if got := mustGet(t, doc, "disabled"); got != false {
		t.Errorf("disabled = %v", got)
	}
}

func TestIntegers(t *testing.T) {
	doc := mustParse(t, `
dec = 42
neg = -17
zero = 0
underscored = 1_000_000
hex = 0xDEADBEEF
oct = 0o755
bin = 0b11010110
`)
	cases := map[string]int64{
		"dec": 42, "neg": -17, "zero": 0, "underscored": 1000000,
		"hex": 0xDEADBEEF, "oct": 0o755, "bin": 0b11010110,
	}
	for k, want := range cases {
		if got := mustGet(t, doc, k); got != want {
			t.Errorf("%s = %v (%T), want %d", k, got, got, want)
		}
	}
}

func TestFloats(t *testing.T) {
	doc := mustParse(t, `
pi = 3.1415
exp = 5e+22
neg_exp = -2.5E-3
pos_inf = inf
neg_inf = -inf
nan_val = nan
`)
	if got := mustGet(t, doc, "pi"); got != 3.1415 {
		t.Errorf("pi = %v", got)
	}
	if got := mustGet(t, doc, "exp"); got != 5e+22 {
		t.Errorf("exp = %v", got)
	}
	if got := mustGet(t, doc, "neg_exp"); got != -2.5e-3 {
		t.Errorf("neg_exp = %v", got)
	}
	if got := mustGet(t, doc, "pos_inf"); !math.IsInf(got.(float64), 1) {
		t.Errorf("pos_inf = %v", got)
	}
	if got := mustGet(t, doc, "neg_inf"); !math.IsInf(got.(float64), -1) {
		t.Errorf("neg_inf = %v", got)
	}
	if got := mustGet(t, doc, "nan_val"); !math.IsNaN(got.(float64)) {
		t.Errorf("nan_val = %v", got)
	}
}

func TestStringEscapes(t *testing.T) {
	doc := mustParse(t, `escaped = "tab\there \"quoted\" \\ unicode \u00e9 \U0001F600"`)
	want := "tab\there \"quoted\" \\ unicode é 😀"
	if got := mustGet(t, doc, "escaped"); got != want {
		t.Errorf("escaped = %q, want %q", got, want)
	}
}

func TestMultilineStrings(t *testing.T) {
	doc := mustParse(t, `
basic = """
first line
second line"""
literal = '''
no \escapes here
'''
folded = """one \
    two"""
`)
	if got := mustGet(t, doc, "basic"); got != "first line\nsecond line" {
		t.Errorf("basic = %q", got)
	}
	if got := mustGet(t, doc, "literal"); got != "no \\escapes here\n" {
		t.Errorf("literal = %q", got)
	}
	if got := mustGet(t, doc, "folded"); got != "one two" {
		t.Errorf("folded = %q, want %q", got, "one two")
	}
}

func TestMultilineStringWithQuotes(t *testing.T) {
	doc := mustParse(t, `s = """a quote: " and two: "" end"""`)
	if got := mustGet(t, doc, "s"); got != `a quote: " and two: "" end` {
		t.Errorf("s = %q", got)
	}
}

func TestDatetimes(t *testing.T) {
	doc := mustParse(t, `
offset = 1979-05-27T07:32:00Z
offset2 = 1979-05-27 07:32:00.999999-08:00
local_dt = 1979-05-27T07:32:00
local_date = 1979-05-27
local_time = 07:32:00
`)
	offset := mustGet(t, doc, "offset").(time.Time)
	if offset.Year() != 1979 || offset.Month() != 5 || offset.Day() != 27 || offset.Hour() != 7 {
		t.Errorf("offset = %v", offset)
	}
	if _, off := mustGet(t, doc, "offset2").(time.Time).Zone(); off != -8*3600 {
		t.Errorf("offset2 zone offset = %d, want -28800", off)
	}
	if got := mustGet(t, doc, "local_date").(time.Time); got.Format("2006-01-02") != "1979-05-27" {
		t.Errorf("local_date = %v", got)
	}
	if got := mustGet(t, doc, "local_time").(time.Time); got.Hour() != 7 || got.Minute() != 32 {
		t.Errorf("local_time = %v", got)
	}
}

func TestArrays(t *testing.T) {
	doc := mustParse(t, `
ints = [1, 2, 3]
nested = [[1, 2], [3, 4]]
strings = [
  "one",
  "two", # trailing comment
]
empty = []
`)
	ints := mustGet(t, doc, "ints").([]any)
	if len(ints) != 3 || ints[0] != int64(1) || ints[2] != int64(3) {
		t.Errorf("ints = %v", ints)
	}
	if got := mustGet(t, doc, "nested.1.0"); got != int64(3) {
		t.Errorf("nested.1.0 = %v", got)
	}
	if got := mustGet(t, doc, "strings.1"); got != "two" {
		t.Errorf("strings.1 = %v", got)
	}
	if got := mustGet(t, doc, "empty").([]any); len(got) != 0 {
		t.Errorf("empty = %v", got)
	}
}

func TestInlineTables(t *testing.T) {
	doc := mustParse(t, `
point = { x = 1, y = 2 }
nested = { a = { b = "deep" }, list = [1, 2] }
`)
	if got := mustGet(t, doc, "point.x"); got != int64(1) {
		t.Errorf("point.x = %v", got)
	}
	if got := mustGet(t, doc, "nested.a.b"); got != "deep" {
		t.Errorf("nested.a.b = %v", got)
	}
	if got := mustGet(t, doc, "nested.list.1"); got != int64(2) {
		t.Errorf("nested.list.1 = %v", got)
	}
}

func TestDottedKeys(t *testing.T) {
	doc := mustParse(t, `
a.b.c = 1
a.b.d = 2
"quoted key".x = "y"
`)
	if got := mustGet(t, doc, "a.b.c"); got != int64(1) {
		t.Errorf("a.b.c = %v", got)
	}
	if got := mustGet(t, doc, "a.b.d"); got != int64(2) {
		t.Errorf("a.b.d = %v", got)
	}
	if got := mustGet(t, doc, "quoted key.x"); got != "y" {
		t.Errorf("quoted key.x = %v", got)
	}
}

func TestTablesAndArraysOfTables(t *testing.T) {
	doc := mustParse(t, `
[server]
host = "localhost"
port = 8080

[server.tls]
enabled = true

[[products]]
name = "Hammer"
sku = 738594937

[[products]]
name = "Nail"
sku = 738594938

[products.details]
weight = "2kg"
`)
	if got := mustGet(t, doc, "server.host"); got != "localhost" {
		t.Errorf("server.host = %v", got)
	}
	if got := mustGet(t, doc, "server.tls.enabled"); got != true {
		t.Errorf("server.tls.enabled = %v", got)
	}
	if got := mustGet(t, doc, "products.0.name"); got != "Hammer" {
		t.Errorf("products.0.name = %v", got)
	}
	if got := mustGet(t, doc, "products.1.sku"); got != int64(738594938) {
		t.Errorf("products.1.sku = %v", got)
	}
	// [products.details] applies to the most recent [[products]] element.
	if got := mustGet(t, doc, "products.1.details.weight"); got != "2kg" {
		t.Errorf("products.1.details.weight = %v", got)
	}
}

func TestOutOfOrderTableDefinition(t *testing.T) {
	// Defining [a.b] first, then [a], is legal TOML.
	doc := mustParse(t, `
[a.b.c]
x = 1

[a]
y = 2
`)
	if got := mustGet(t, doc, "a.b.c.x"); got != int64(1) {
		t.Errorf("a.b.c.x = %v", got)
	}
	if got := mustGet(t, doc, "a.y"); got != int64(2) {
		t.Errorf("a.y = %v", got)
	}
}

func TestGetMissing(t *testing.T) {
	doc := mustParse(t, "a = 1\n")
	if _, ok := doc.Get("missing"); ok {
		t.Error("Get(missing) should be false")
	}
	if _, ok := doc.Get("a.b"); ok {
		t.Error("Get(a.b) through a scalar should be false")
	}
	if _, ok := doc.Get(""); ok {
		t.Error("Get(\"\") should be false")
	}
}

func TestToJSON(t *testing.T) {
	doc := mustParse(t, `
[server]
port = 8080
`)
	out, err := doc.ToJSON()
	if err != nil {
		t.Fatalf("ToJSON failed: %v", err)
	}
	if !strings.Contains(string(out), `"port": 8080`) {
		t.Errorf("ToJSON output missing port: %s", out)
	}
}

func TestKeys(t *testing.T) {
	doc := mustParse(t, "b = 1\na = 2\n")
	keys := doc.Keys()
	if len(keys) != 2 || keys[0] != "a" || keys[1] != "b" {
		t.Errorf("Keys() = %v", keys)
	}
}

func TestErrors(t *testing.T) {
	cases := map[string]string{
		"duplicate key":     "a = 1\na = 2\n",
		"duplicate dotted":  "a.b = 1\na.b = 2\n",
		"value into table":  "a = 1\na.b = 2\n",
		"table into value":  "a = 1\n[a]\n",
		"redefined table":   "[a]\nx = 1\n[a]\ny = 2\n",
		"dotted then table": "a.b = 1\n[a.b]\n",
		"mixed array":       "a = [1, \"two\"]\n",
		"bad integer":       "a = 01\n",
		"bad float":         "a = 1.2.3\n",
		"bare value":        "a = hello\n",
		"missing equals":    "a 1\n",
		"junk after value":  "a = 1 junk\n",
		"junk after header": "[a] junk\n",
		"unterminated str":  "a = \"oops\n",
		"unterminated arr":  "a = [1, 2\n",
		"bad escape":        `a = "\q"` + "\n",
		"newline in inline": "a = { x = 1,\n y = 2 }\n",
		"extend inline":     "a = { x = 1 }\na.y = 2\n",
		"invalid datetime":  "a = 1979-13-40\n",
	}
	for name, src := range cases {
		if _, err := ParseString(src); err == nil {
			t.Errorf("%s: expected an error, got none (input %q)", name, src)
		}
	}
}

func TestParseErrorHasPosition(t *testing.T) {
	_, err := ParseString("a = 1\nb = 2\nb = 3\n")
	if err == nil {
		t.Fatal("expected an error")
	}
	perr, ok := err.(*ParseError)
	if !ok {
		t.Fatalf("error is %T, want *ParseError", err)
	}
	if perr.Line != 3 {
		t.Errorf("error line = %d, want 3", perr.Line)
	}
	if !strings.Contains(perr.Message, "duplicate key") {
		t.Errorf("error message = %q", perr.Message)
	}
}

func TestCommentsAndWhitespace(t *testing.T) {
	doc := mustParse(t, "# leading comment\n\n  a = 1 # trailing comment\n\tb\t=\t2\n")
	if got := mustGet(t, doc, "a"); got != int64(1) {
		t.Errorf("a = %v", got)
	}
	if got := mustGet(t, doc, "b"); got != int64(2) {
		t.Errorf("b = %v", got)
	}
}

func TestCRLF(t *testing.T) {
	doc := mustParse(t, "a = 1\r\nb = 2\r\n")
	if got := mustGet(t, doc, "b"); got != int64(2) {
		t.Errorf("b = %v", got)
	}
}

func TestFullExample(t *testing.T) {
	// The canonical example from the TOML README, trimmed.
	doc := mustParse(t, `
title = "TOML Example"

[owner]
name = "Tom Preston-Werner"
dob = 1979-05-27T07:32:00-08:00

[database]
enabled = true
ports = [ 8000, 8001, 8002 ]
data = [ ["delta", "phi"], [3.14] ]
temp_targets = { cpu = 79.5, case = 72.0 }

[servers]

  [servers.alpha]
  ip = "10.0.0.1"
  role = "frontend"

  [servers.beta]
  ip = "10.0.0.2"
  role = "backend"
`)
	if got := mustGet(t, doc, "owner.name"); got != "Tom Preston-Werner" {
		t.Errorf("owner.name = %v", got)
	}
	if got := mustGet(t, doc, "database.ports.2"); got != int64(8002) {
		t.Errorf("ports.2 = %v", got)
	}
	if got := mustGet(t, doc, "servers.beta.role"); got != "backend" {
		t.Errorf("servers.beta.role = %v", got)
	}
}
