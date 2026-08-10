package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/visdomtech/schemachecker-go/internal/checkererror"
	"github.com/visdomtech/schemachecker-go/internal/dirdiff"
	"github.com/visdomtech/schemachecker-go/internal/pgdump"
)

// RunValidate executes the validate subcommand.
// Usage: validate [schemaDefinitionMigrations] [incrementalMigrations] [outputDirectory]
func RunValidate(args []string) error {
	if len(args) != 4 {
		return UsageError("Invalid command line arguments.\nUsage validate [schemaDefinitionMigrations] [incrementalMigarations] [outputDirectory]")
	}

	schemaDefMigrations := args[1]
	incrementalMigrations := args[2]
	outputDir := args[3]

	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}

	exportedFromMigrations := filepath.Join(outputDir, "incrementalMigrations.sql")
	exportedFromSchema := filepath.Join(outputDir, "schemaDefinitionMigrations.sql")

	schemaOnly := os.Getenv("DUMP_DATA") == ""
	ctx := context.Background()

	fmt.Printf("Exporting incremental migrations [%s]\n", incrementalMigrations)
	if err := pgdump.ProvisionAndDump(ctx, incrementalMigrations, exportedFromMigrations, "", schemaOnly); err != nil {
		return err
	}

	fmt.Printf("Exporting schema definition migrations [%s]\n", schemaDefMigrations)
	if err := pgdump.ProvisionAndDump(ctx, schemaDefMigrations, exportedFromSchema, "", schemaOnly); err != nil {
		return err
	}

	diffResult, isSame, err := dirdiff.DiffFiles(
		exportedFromSchema, exportedFromMigrations,
		schemaDefMigrations, incrementalMigrations,
	)
	if err != nil {
		return err
	}

	if isSame {
		fmt.Println("The schemas are identical")
		return nil
	}

	fmt.Println(diffResult)
	return checkererror.New(1, "The schemas are DIFFERENT")
}
