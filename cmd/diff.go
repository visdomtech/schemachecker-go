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
	fmt.Printf("[1/6] Copying baseline [%s]\n", baselineFile)
	baselineMigrationFolder := filepath.Join(outputDir, "baselineMigrations")
	if err := os.MkdirAll(baselineMigrationFolder, 0o755); err != nil {
		return checkererror.Wrap(checkererror.ExitInfra, err, "create baseline migration folder: %s", err)
	}
	baselineMigrationFile := filepath.Join(baselineMigrationFolder, "V1.0.0__baseline.sql")
	if err := copyFile(baselineFile, baselineMigrationFile); err != nil {
		return checkererror.Wrap(checkererror.ExitInfra, err, "copy baseline file: %s", err)
	}

	// NOTE: ProvisionAndDump provisions a separate PostgreSQL testcontainer per call
	// (keyed by migration directory). Both containers run concurrently until process exit.
	// Ensure the CI runner has sufficient memory (≥2GB recommended).

	// 2. Dump incremental migrations
	incrementalDump := filepath.Join(outputDir, "incrementalDump.sql")
	fmt.Printf("[2/6] Dumping incremental migrations [%s]\n", migrationsDir)
	if err := pgdump.ProvisionAndDump(ctx, migrationsDir, incrementalDump, schemaOnly); err != nil {
		return checkererror.Wrap(checkererror.ExitInfra, err, "dump incremental migrations: %s", err)
	}
	fmt.Printf("[2/6] Incremental dump complete\n")

	// 3. Split incremental dump
	fmt.Printf("[3/6] Splitting incremental dump\n")
	incrementalSplit := filepath.Join(outputDir, "incrementalsplit")
	if err := split.Dump(incrementalDump, incrementalSplit, split.Options{}); err != nil {
		return checkererror.Wrap(checkererror.ExitInfra, err, "split incremental dump: %s", err)
	}

	// 4. Dump baseline
	baselineDump := filepath.Join(outputDir, "baselineDump.sql")
	fmt.Printf("[4/6] Dumping baseline [%s]\n", baselineFile)
	if err := pgdump.ProvisionAndDump(ctx, baselineMigrationFolder, baselineDump, schemaOnly); err != nil {
		return checkererror.Wrap(checkererror.ExitInfra, err, "dump baseline: %s", err)
	}
	fmt.Printf("[4/6] Baseline dump complete\n")

	// 5. Split baseline dump
	fmt.Printf("[5/6] Splitting baseline dump\n")
	baselineSplit := filepath.Join(outputDir, "baselinesplit")
	if err := split.Dump(baselineDump, baselineSplit, split.Options{}); err != nil {
		return checkererror.Wrap(checkererror.ExitInfra, err, "split baseline dump: %s", err)
	}

	// 6. Diff the two splits
	fmt.Printf("[6/6] Comparing schemas\n")
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

// copyFile copies a file from src to dst, returning errors with path context.
func copyFile(src, dst string) (err error) {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open source %q: %w", src, err)
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("create destination %q: %w", dst, err)
	}
	defer func() {
		if cerr := out.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()

	if _, err := io.Copy(out, in); err != nil {
		return fmt.Errorf("copy %q to %q: %w", src, dst, err)
	}
	return nil
}
