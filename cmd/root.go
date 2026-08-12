// Package cmd implements the CLI subcommands for schemachecker.
package cmd

import (
	"fmt"
	"os"

	"github.com/visdomtech/schemachecker-go/internal/checkererror"
)

const usage = `Usage [command] [opts]
Where command:
    - check
    - validate
    - split
    - merge
    - orphaned
    - dump
    - dirdiff
Options:
    --version, -v    Print version and exit
`

// PrintUsage writes the usage text to stderr.
func PrintUsage() {
	fmt.Fprint(os.Stderr, usage)
}

// UsageError returns a checkererror.Error with exit code 99 for invalid arguments.
func UsageError(msg string) error {
	return checkererror.New(checkererror.ExitUsage, "%s\n%s", msg, usage)
}
