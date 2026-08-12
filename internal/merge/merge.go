// Package merge implements merging SQL files referenced from an index file
// into a single migration file.
package merge

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// FromIndex reads the index file and writes all referenced SQL files
// concatenated into the migration file.
func FromIndex(indexFile, migrationFile string) error {
	data, err := os.ReadFile(indexFile)
	if err != nil {
		return fmt.Errorf("read index file: %w", err)
	}
	content := string(data)

	root := filepath.Dir(indexFile)
	absRoot, _ := filepath.Abs(root)

	out, err := os.Create(migrationFile)
	if err != nil {
		return fmt.Errorf("create migration file: %w", err)
	}

	w := bufio.NewWriter(out)

	for _, line := range strings.Split(strings.TrimSuffix(content, "\n"), "\n") {
		if strings.HasPrefix(line, "--") {
			if _, err := fmt.Fprintln(w, line); err != nil {
				out.Close()
				return fmt.Errorf("write comment: %w", err)
			}
		} else if strings.TrimSpace(line) == "" {
			if _, err := fmt.Fprintln(w, line); err != nil {
				out.Close()
				return fmt.Errorf("write blank line: %w", err)
			}
		} else {
			trimmed := strings.TrimSpace(line)
			// Clean the entry to normalize . and .. before joining
			trimmed = filepath.Clean(trimmed)
			filePath := filepath.Join(root, trimmed)

			// Path containment: ensure resolved path stays within root
			absPath, _ := filepath.Abs(filePath)
			if !strings.HasPrefix(absPath, absRoot+string(os.PathSeparator)) {
				out.Close()
				return fmt.Errorf("index entry %q escapes base directory %s", trimmed, root)
			}

			if err := requireFileReadable(filePath, indexFile); err != nil {
				out.Close()
				return err
			}

			fileData, err := os.ReadFile(filePath)
			if err != nil {
				out.Close()
				return fmt.Errorf("read referenced file %s: %w", filePath, err)
			}

			if _, err := fmt.Fprintf(w, "\n-- including %s\n", line); err != nil {
				out.Close()
				return fmt.Errorf("write including header: %w", err)
			}
			if _, err := fmt.Fprintln(w, string(fileData)); err != nil {
				out.Close()
				return fmt.Errorf("write file content: %w", err)
			}
		}
	}

	if _, err := fmt.Fprintln(w, "SET check_function_bodies = true; -- reset check_function_bodies"); err != nil {
		out.Close()
		return fmt.Errorf("write footer: %w", err)
	}

	if err := w.Flush(); err != nil {
		out.Close()
		return fmt.Errorf("flush migration file: %w", err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("close migration file: %w", err)
	}
	return nil
}

func requireFileReadable(path, indexFile string) error {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return fmt.Errorf("couldn't find or read the file %s referenced from the index file %s", path, indexFile)
	}
	return nil
}
