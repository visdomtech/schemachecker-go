package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/visdomtech/schemachecker-go/internal/checkererror"
	"github.com/visdomtech/schemachecker-go/internal/pgdump"
)

// RunDump executes the dump subcommand.
// Usage: dump [migrations] [outputFile]
func RunDump(args []string) error {
	if len(args) != 3 {
		return UsageError("Invalid command line arguments.\nUsage dump [migrations] [outputFile]")
	}

	migrations := args[1]
	outputFile := args[2]
	schemaOnly := os.Getenv("DUMP_DATA") == ""

	// Validate output path: parent directory must exist and be writable
	outDir := filepath.Dir(outputFile)
	if info, err := os.Stat(outDir); err != nil || !info.IsDir() {
		return checkererror.New(checkererror.ExitUsage, "output directory does not exist: %s", outDir)
	}

	fmt.Printf("Exporting migrations [%s]\n", migrations)

	return pgdump.ProvisionAndDump(context.Background(), migrations, outputFile, schemaOnly)
}
