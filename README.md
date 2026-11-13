# go-toml-parser

A zero-dependency TOML v1.0 parser for Go, with a small CLI (`tomlq`) to
query and convert TOML files to JSON.

## The problem

Go's standard library has no TOML support, and most TOML libraries pull in
dependencies or decode into structs via reflection. Sometimes you just want
to read a config file: parse it, pull one value out by path, or dump it as
JSON — with errors that tell you exactly where the file went wrong.

## Design decisions

1. **Hand-written recursive-descent parser, no lexer.** TOML's value grammar
   (datetimes, numbers, multi-line strings) is easier to classify in
   context than to tokenize up front, so the parser works directly on the
   input with a position-tracking cursor.
2. **Strict where TOML is strict.** Duplicate keys, redefined tables,
   mixed-type arrays, and extending an inline table are all errors — not
   silent overwrites. The parser tracks how every table came into being
   (header, dotted key, inline table, array of tables) so redefinition
   rules match the spec, including out-of-order definitions like `[a.b]`
   before `[a]`.
3. **Plain Go values, no reflection.** A document decodes to `string`,
   `int64`, `float64`, `bool`, `time.Time`, `[]any`, and `map[string]any`,
   so it composes with `encoding/json` and ordinary code. Offset datetimes
   keep their location; local dates are UTC midnight; local times use year 0.
4. **Errors carry line and column.** Every syntax error is a `*ParseError`
   with the position where parsing failed, so a bad config file points at
   the offending line instead of failing silently.

## Usage

### Library

```go
import "github.com/Subhanmalik138/go-toml-parser/toml"

doc, err := toml.ParseFile("config.toml")
if err != nil {
    log.Fatal(err) // toml: line 12, column 9: duplicate key "server.port"
}

port, ok := doc.Get("server.port")   // any, bool
host, _ := doc.Get("server.host")    // dotted paths; "ports.1" indexes arrays

out, err := doc.ToJSON()             // indented JSON of the whole document
```

### CLI

```bash
go build -o tomlq ./cmd/tomlq

tomlq config.toml                    # whole document as JSON
tomlq -get server.tls.enabled config.toml
tomlq -validate config.toml          # exit 0 when the file parses
```

## Supported TOML

- Key/value pairs, bare / `"basic"` / `'literal'` keys, dotted keys
- Basic and literal strings, single- and multi-line, with escapes
  (`\b \t \n \f \r \" \\ \uXXXX \UXXXXXXXX`) and line-ending backslash
  folding in multi-line basic strings
- Integers (decimal, `0x`/`0o`/`0b`, underscores, signs), floats
  (exponents, `inf`, `nan`), booleans
- All four datetimes: offset datetime, local datetime, local date, local time
- Arrays (nested, multi-line, trailing commas) and inline tables
- Tables `[a.b]`, arrays of tables `[[a.b]]`, comments, CRLF line endings

## Run the tests

```bash
go test ./...
```

## Tech stack

- Go 1.23, standard library only — no third-party dependencies.
