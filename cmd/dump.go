package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/visdomtech/schemachecker-go/internal/checkererror"
	"github.com/visdomtech/schemachecker-go/internal/pgdump"
	"github.com/visdomtech/schemachecker-go/internal/split"
)

// RunDump executes the dump subcommand.
// Usage: dump [migrations] [outputPath] [--split]
//
// Without --split, outputPath is a single SQL file.
// With --split, outputPath is a directory; the dump is split into
// per-object files with merge (table objects inlined) enabled.
func RunDump(args []string) error {
	if len(args) < 3 || len(args) > 4 {
		return UsageError("Invalid command line arguments.\nUsage dump [migrations] [outputPath] [--split]")
	}

	migrations := args[1]
	outputPath := args[2]
	doSplit := false
	if len(args) == 4 {
		if args[3] != "--split" {
			return UsageError("Invalid command line arguments.\nUsage dump [migrations] [outputPath] [--split]")
		}
		doSplit = true
	}

	schemaOnly := os.Getenv("DUMP_DATA") == ""

	if doSplit {
		return dumpAndSplit(context.Background(), migrations, outputPath, schemaOnly)
	}

	// Validate output path: parent directory must exist and be writable
	outDir := filepath.Dir(outputPath)
	if info, err := os.Stat(outDir); err != nil || !info.IsDir() {
		return checkererror.New(checkererror.ExitUsage, "output directory does not exist: %s", outDir)
	}

	fmt.Printf("Exporting migrations [%s]\n", migrations)

	return pgdump.ProvisionAndDump(context.Background(), migrations, outputPath, schemaOnly)
}

// dumpAndSplit dumps migrations to a temporary SQL file, then splits it
// into per-object files under outputDir with merge enabled.
func dumpAndSplit(ctx context.Context, migrations, outputDir string, schemaOnly bool) error {
	tmpFile, err := os.CreateTemp("", "schemachecker-dump-*.sql")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	tmpFile.Close()
	defer os.Remove(tmpPath)

	fmt.Printf("Exporting migrations [%s]\n", migrations)

	if err := pgdump.ProvisionAndDump(ctx, migrations, tmpPath, schemaOnly); err != nil {
		return err
	}

	fmt.Printf("Splitting into [%s]\n", outputDir)

	return split.Dump(tmpPath, outputDir, split.Options{Merge: true})
}
