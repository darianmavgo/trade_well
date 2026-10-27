// Package clicolor holds what every schwaber CLI command package shares:
// ANSI colors, the banner, and the "already printed" error. It is a leaf
// library and imports nothing else from this module.
package clicolor

import (
	"errors"
	"fmt"
	"io"
)

// ANSI colors used by CLI command output.
const (
	Reset  = "\033[0m"
	Red    = "\033[31m"
	Green  = "\033[32m"
	Yellow = "\033[33m"
	Blue   = "\033[34m"
	Cyan   = "\033[36m"
	Bold   = "\033[1m"
	Dim    = "\033[2m"
	White  = "\033[37m"
)

// ErrReported means the command already printed what went wrong. The caller
// should exit non-zero without printing anything more.
var ErrReported = errors.New("already reported")

// Banner prints the schwaber title line.
func Banner(out io.Writer) {
	fmt.Fprintf(out, "%s%s=== Schwaber - Charles Schwab Trading & Data Engine ===%s\n\n", Bold, Cyan, Reset)
}
