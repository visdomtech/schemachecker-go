package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/visdomtech/schemachecker-go/internal/checkererror"
	"github.com/visdomtech/schemachecker-go/internal/dirdiff"
	"github.com/visdomtech/schemachecker-go/internal/merge"
	"github.com/visdomtech/schemachecker-go/internal/pgdump"
	"github.com/visdomtech/schemachecker-go/internal/split"
)

// RunCheck executes the check subcommand.
// Usage: check [schemaDefinitionIndex] [incrementalMigrations] [outputDirectory]
func RunCheck(args []string) error {
	if len(args) != 4 {
		return UsageError("Invalid command line arguments.\nUsage check [schemaDefinitionIndex] [incrementalMigrations] [outputDirectory]")
	}

	schemaIndexFile := args[1]
	incrementalMigrations := args[2]
	outputDir := args[3]

	// Safety check: refuse to remove dangerous directories
	absOut, err := filepath.Abs(outputDir)
	if err != nil {
		return checkererror.Wrap(checkererror.ExitUsage, err, "resolve output directory: %s", err)
	}
	cwd, _ := os.Getwd()
	home, _ := os.UserHomeDir()
	if absOut == "/" || absOut == cwd || absOut == home {
		return checkererror.New(checkererror.ExitUsage, "refusing to remove dangerous output directory: %s", outputDir)
	}

	// Clean output directory to prevent corruption from partial retries
	if err := os.RemoveAll(outputDir); err != nil {
		return checkererror.Wrap(checkererror.ExitInfra, err, "clean output directory: %s", err)
	}
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return checkererror.Wrap(checkererror.ExitInfra, err, "create output directory: %s", err)
	}

	ctx := context.Background()
	schemaOnly := os.Getenv("DUMP_DATA") == ""

	// 1. Create a migration file from the schema definition
	fmt.Printf("Creating migration file from schema definition %s\n", schemaIndexFile)
	schemaMigrationFolder := filepath.Join(outputDir, "schemaMigrations")
	if err := os.MkdirAll(schemaMigrationFolder, 0o755); err != nil {
		return checkererror.Wrap(checkererror.ExitInfra, err, "create schema migration folder: %s", err)
	}
	migrationFile := filepath.Join(schemaMigrationFolder, "V1.0.0__migrationFile.sql")
	if err := merge.FromIndex(schemaIndexFile, migrationFile); err != nil {
		return checkererror.Wrap(checkererror.ExitInfra, err, "merge schema definition: %s", err)
	}

	// 2. Create a dump from the migration file
	schemaDump := filepath.Join(outputDir, "schemaDump.sql")
	if err := pgdump.ProvisionAndDump(ctx, schemaMigrationFolder, schemaDump, schemaOnly); err != nil {
		return checkererror.Wrap(checkererror.ExitInfra, err, "dump schema definition: %s", err)
	}

	// 3. Split the dump
	schemaSplit := filepath.Join(outputDir, "schemasplit")
	if err := split.Dump(schemaDump, schemaSplit); err != nil {
		return checkererror.Wrap(checkererror.ExitInfra, err, "split schema dump: %s", err)
	}

	// 4. Create a dump of the incremental migrations
	incrementalDump := filepath.Join(outputDir, "incrementalDump.sql")
	if err := pgdump.ProvisionAndDump(ctx, incrementalMigrations, incrementalDump, schemaOnly); err != nil {
		return checkererror.Wrap(checkererror.ExitInfra, err, "dump incremental migrations: %s", err)
	}

	// 5. Split the incremental dump
	incrementalSplit := filepath.Join(outputDir, "incrementalsplit")
	if err := split.Dump(incrementalDump, incrementalSplit); err != nil {
		return checkererror.Wrap(checkererror.ExitInfra, err, "split incremental dump: %s", err)
	}

	// 6. Diff the two splits
	differ, err := dirdiff.New(schemaSplit, incrementalSplit, nil)
	if err != nil {
		return checkererror.Wrap(checkererror.ExitInfra, err, "compare schemas: %s", err)
	}

	if differ.IsSame() {
		fmt.Println("The schemas are the same")
		return nil
	}

	differ.Dump()
	return checkererror.New(checkererror.ExitDiff, "The schemas are not the same")
}
