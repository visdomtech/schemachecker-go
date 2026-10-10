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
// Objects that are not table-owned keep their own files: POLICY, ROW SECURITY,
// COMMENT, RULE, FUNCTION, VIEW, MATERIALIZED VIEW, FOREIGN TABLE, TABLE ATTACH,
// INDEX ATTACH, DATA, EXTENSION, SCHEMAS and SEQUENCE_SET. Table-owned children
// normally merge away, but an INDEX or TRIGGER whose ON clause does not resolve to
// a table file (materialized views, partitioned parents via ON ONLY, relations
// whose name needs quoting) and a DEFAULT or FK CONSTRAINT filed under a quoted
// relation name stay in their own directory. A SEQUENCE keeps its own file unless
// it is a standard OWNED BY sequence on a plain integer/bigint NOT NULL column,
// which is inlined as a bigserial column type (IDENTITY sequences, sequences with
// CYCLE / explicit MINVALUE / MAXVALUE, and sequences whose owning nextval default
// could not be removed from every file holding it are always preserved).
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
