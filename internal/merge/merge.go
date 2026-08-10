// Package merge implements merging SQL files referenced from an index file
// into a single migration file.
package merge

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/visdomtech/schemachecker-go/internal/split"
)

// FromIndex reads the index file and writes all referenced SQL files
// concatenated into the migration file.
func FromIndex(indexFile, migrationFile string) error {
	content, err := split.ReadAll(indexFile)
	if err != nil {
		return fmt.Errorf("read index file: %w", err)
	}

	root := filepath.Dir(indexFile)

	out, err := os.Create(migrationFile)
	if err != nil {
		return fmt.Errorf("create migration file: %w", err)
	}
	defer out.Close()

	for _, line := range strings.Split(strings.TrimSuffix(content, "\n"), "\n") {
		if strings.HasPrefix(line, "--") {
			fmt.Fprintln(out, line)
		} else if strings.TrimSpace(line) == "" {
			fmt.Fprintln(out, line)
		} else {
			trimmed := strings.TrimSpace(line)
			filePath := filepath.Join(root, trimmed)

			if err := requireFileReadable(filePath, indexFile); err != nil {
				return err
			}

			fileContent, err := split.ReadAll(filePath)
			if err != nil {
				return fmt.Errorf("read referenced file %s: %w", filePath, err)
			}

			fmt.Fprintf(out, "\n-- including %s\n", line)
			fmt.Fprintln(out, fileContent)
		}
	}

	fmt.Fprintln(out, "SET check_function_bodies = true; -- reset check_function_bodies")
	return nil
}

func requireFileReadable(path, indexFile string) error {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return fmt.Errorf("couldn't find or read the file %s referenced from the index file %s", path, indexFile)
	}
	return nil
}
