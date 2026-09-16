package cmd

import (
	"github.com/visdomtech/schemachecker-go/internal/split"
)

// RunSplit executes the split subcommand.
// Usage: split [schemaExportFile] [outputDir]
//
// Table-associated objects (INDEX, TRIGGER, DEFAULT, CONSTRAINT,
// FK_CONSTRAINT) are always merged into the TABLE file.
func RunSplit(args []string) error {
	if len(args) != 3 {
		return UsageError("Invalid command line arguments.\nUsage split [schemaExportFile] [outputDir]")
	}

	outputDir := args[2]
	if err := cleanOutputDir(outputDir); err != nil {
		return err
	}

	return split.Dump(args[1], outputDir, split.Options{Merge: true})
}
