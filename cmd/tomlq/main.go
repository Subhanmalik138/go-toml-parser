// Command tomlq parses TOML and prints it as JSON, or fetches one value
// by dotted key path. It is a thin shell over the toml package; all parsing
// lives in the library.
//
// Usage:
//
//	tomlq file.toml                 print the whole document as JSON
//	tomlq -get server.port file.toml
//	                                print one value (strings raw, the rest JSON)
//	tomlq -validate file.toml       exit 0 when the file parses, 1 otherwise
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/Subhanmalik138/go-toml-parser/toml"
)

func main() {
	get := flag.String("get", "", "dotted key path to print, e.g. server.tls.enabled")
	validate := flag.Bool("validate", false, "only check that the file parses")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: tomlq [-get dotted.path] [-validate] file.toml\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}
	path := flag.Arg(0)

	doc, err := toml.ParseFile(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if *validate {
		fmt.Println("valid TOML")
		return
	}
	if *get != "" {
		v, ok := doc.Get(*get)
		if !ok {
			fmt.Fprintf(os.Stderr, "tomlq: key %q not found\n", *get)
			os.Exit(1)
		}
		fmt.Println(toml.FormatValue(v))
		return
	}
	out, err := doc.ToJSON()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(string(out))
}
