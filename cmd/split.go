package cmd

import (
	"github.com/visdomtech/schemachecker-go/internal/split"
)

// RunSplit executes the split subcommand.
// Usage: split [schemaExportFile] [outputDir]
//
// Splits a pg_dump SQL file into per-object files without merge,
// preserving the raw pg_dump representation for round-trip consistency.
func RunSplit(args []string) error {
	if len(args) != 3 {
		return UsageError("Invalid command line arguments.\nUsage split [schemaExportFile] [outputDir]")
	}

	outputDir := args[2]
	if err := cleanOutputDir(outputDir); err != nil {
		return err
	}

	return split.Dump(args[1], outputDir, split.Options{})
}
