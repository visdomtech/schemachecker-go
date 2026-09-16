// Package cmd implements the CLI subcommands for schemachecker.
package cmd

import (
	"fmt"
	"os"
	"path/filepath"

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
    - diff
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

// cleanOutputDir removes and recreates the output directory after verifying
// it is not a dangerous path (root, cwd, home, system directories).
func cleanOutputDir(outputDir string) error {
	absOut, err := filepath.Abs(outputDir)
	if err != nil {
		return checkererror.Wrap(checkererror.ExitUsage, err, "resolve output directory: %s", err)
	}
	// Resolve symlinks so the blocklist comparison matches what RemoveAll will actually touch.
	// If the path doesn't exist yet, EvalSymlinks may fail — fall back to the unresolved path.
	resolved := absOut
	if r, err := filepath.EvalSymlinks(absOut); err == nil {
		resolved = r
	}
	cwd, _ := os.Getwd()
	home, _ := os.UserHomeDir()
	dangerous := map[string]bool{"/": true, cwd: true, home: true, "/etc": true, "/usr": true, "/var": true, "/tmp": true, "/boot": true}
	if dangerous[resolved] {
		return checkererror.New(checkererror.ExitUsage, "refusing to remove dangerous output directory: %s", outputDir)
	}

	if err := os.RemoveAll(outputDir); err != nil {
		return checkererror.Wrap(checkererror.ExitInfra, err, "clean output directory: %s", err)
	}
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return checkererror.Wrap(checkererror.ExitInfra, err, "create output directory: %s", err)
	}
	return nil
}
