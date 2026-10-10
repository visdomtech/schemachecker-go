package cmd

import (
	"github.com/visdomtech/schemachecker-go/internal/split"
)

// RunSplit executes the split subcommand.
// Usage: split [schemaExportFile] [outputDir]
//
// Splits a pg_dump SQL file into per-object files. The output is always merged:
// table-owned INDEX, TRIGGER, DEFAULT and FK_CONSTRAINT SQL is inlined into the
// owning <schema>/TABLE/<table>.sql, and those per-type child files are removed
// (see split.MergeTableObjects).
//
// Objects that are not table-owned keep their own files: POLICY, FUNCTION, VIEW,
// DATA, EXTENSION, SCHEMAS and SEQUENCE_SET. A SEQUENCE keeps its own file unless
// it is a standard OWNED BY sequence, which is inlined as a bigserial column type
// (IDENTITY sequences are always preserved).
func RunSplit(args []string) error {
	if len(args) != 3 {
		return UsageError("Invalid command line arguments.\nUsage split [schemaExportFile] [outputDir]")
	}

	outputDir := args[2]
	if err := cleanOutputDir(outputDir); err != nil {
		return err
	}

	return split.Dump(args[1], outputDir)
}
