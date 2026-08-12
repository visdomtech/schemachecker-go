package cmd

import (
	"github.com/visdomtech/schemachecker-go/internal/split"
)

// RunSplit executes the split subcommand.
// Usage: split [schemaExportFile] [outputDir] [--merge]
func RunSplit(args []string) error {
	if len(args) < 3 || len(args) > 4 {
		return UsageError("Invalid command line arguments.\nUsage split [schemaExportFile] [outputDir] [--merge]")
	}

	opts := split.Options{}
	if len(args) == 4 {
		if args[3] != "--merge" {
			return UsageError("Invalid command line arguments.\nUsage split [schemaExportFile] [outputDir] [--merge]")
		}
		opts.Merge = true
	}

	return split.Dump(args[1], args[2], opts)
}
