package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/visdomtech/schemachecker-go/internal/checkererror"
	"github.com/visdomtech/schemachecker-go/internal/dirdiff"
	"github.com/visdomtech/schemachecker-go/internal/pgdump"
	"github.com/visdomtech/schemachecker-go/internal/split"
)

// RunDiff executes the diff subcommand.
// Usage: diff [migrationsDir] [baselineFile] [outputDir]
func RunDiff(args []string) error {
	if len(args) != 4 {
		return UsageError("Invalid command line arguments.\nUsage diff [migrationsDir] [baselineFile] [outputDir]")
	}

	migrationsDir := args[1]
	baselineFile := args[2]
	outputDir := args[3]

	// Safety check: refuse dangerous directories
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

	// 1. Wrap the baseline SQL file into a migration folder
	baselineMigrationFolder := filepath.Join(outputDir, "baselineMigrations")
	if err := os.MkdirAll(baselineMigrationFolder, 0o755); err != nil {
		return checkererror.Wrap(checkererror.ExitInfra, err, "create baseline migration folder: %s", err)
	}
	baselineMigrationFile := filepath.Join(baselineMigrationFolder, "V1.0.0__baseline.sql")
	if err := copyFile(baselineFile, baselineMigrationFile); err != nil {
		return checkererror.Wrap(checkererror.ExitInfra, err, "copy baseline file: %s", err)
	}

	// 2. Dump incremental migrations
	incrementalDump := filepath.Join(outputDir, "incrementalDump.sql")
	fmt.Printf("Exporting incremental migrations [%s]\n", migrationsDir)
	if err := pgdump.ProvisionAndDump(ctx, migrationsDir, incrementalDump, schemaOnly); err != nil {
		return checkererror.Wrap(checkererror.ExitInfra, err, "dump incremental migrations: %s", err)
	}

	// 3. Split incremental dump
	incrementalSplit := filepath.Join(outputDir, "incrementalsplit")
	if err := split.Dump(incrementalDump, incrementalSplit, split.Options{}); err != nil {
		return checkererror.Wrap(checkererror.ExitInfra, err, "split incremental dump: %s", err)
	}

	// 4. Dump baseline
	baselineDump := filepath.Join(outputDir, "baselineDump.sql")
	fmt.Printf("Exporting baseline [%s]\n", baselineFile)
	if err := pgdump.ProvisionAndDump(ctx, baselineMigrationFolder, baselineDump, schemaOnly); err != nil {
		return checkererror.Wrap(checkererror.ExitInfra, err, "dump baseline: %s", err)
	}

	// 5. Split baseline dump
	baselineSplit := filepath.Join(outputDir, "baselinesplit")
	if err := split.Dump(baselineDump, baselineSplit, split.Options{}); err != nil {
		return checkererror.Wrap(checkererror.ExitInfra, err, "split baseline dump: %s", err)
	}

	// 6. Diff the two splits
	differ, err := dirdiff.New(incrementalSplit, baselineSplit, nil)
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

// copyFile copies a file from src to dst.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}
