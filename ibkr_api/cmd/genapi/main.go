// genapi regenerates pkg/ibkr/api_gen.go from api-reference.json:
//
//	go run ./cmd/genapi [-spec api-reference.json] [-out pkg/ibkr/api_gen.go]
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/darianmavgo/ibkr_api/pkg/apigen"
)

func main() {
	specPath := flag.String("spec", "api-reference.json", "OpenAPI document")
	outPath := flag.String("out", "pkg/ibkr/api_gen.go", "generated Go file")
	flag.Parse()
	data, err := os.ReadFile(*specPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	src, err := apigen.Generate(data)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.WriteFile(*outPath, append(src, '\n'), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("wrote %s (%d bytes)\n", *outPath, len(src))
}
