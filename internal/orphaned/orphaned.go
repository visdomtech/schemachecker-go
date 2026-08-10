// Package orphaned detects files not referenced from an index file
// and files referenced but not present on the filesystem.
package orphaned

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/visdomtech/schemachecker-go/internal/split"
)

// FromIndex validates that all files referenced in the index file exist
// on disk, and that all files on disk are referenced from the index.
func FromIndex(indexFile string) error {
	content, err := split.ReadAll(indexFile)
	if err != nil {
		return fmt.Errorf("read index file: %w", err)
	}

	root := filepath.Dir(indexFile)

	// Collect referenced files from index (non-comment, non-blank lines)
	referenced := make(map[string]bool)
	for _, line := range strings.Split(strings.TrimSuffix(content, "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "--") {
			continue
		}
		referenced[trimmed] = true
	}

	// Walk the directory tree to find actual files
	actual := make(map[string]bool)
	err = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		actual[rel] = true
		return nil
	})
	if err != nil {
		return fmt.Errorf("walk directory: %w", err)
	}

	// Remove the index file itself from the actual set
	indexBase := filepath.Base(indexFile)
	delete(actual, indexBase)

	inFSNotInIndex := missingFromFirst(referenced, actual)
	inIndexNotInFS := missingFromFirst(actual, referenced)

	if len(inFSNotInIndex) == 0 && len(inIndexNotInFS) == 0 {
		return nil
	}

	hasError := false

	if len(inIndexNotInFS) > 0 {
		fmt.Fprintln(os.Stderr, "Files missing in filesystem but referenced from index file: ")
		for _, m := range sortedKeys(inIndexNotInFS) {
			fmt.Fprintf(os.Stderr, "\t%s\n", m)
		}
		hasError = true
	}

	if len(inFSNotInIndex) > 0 {
		fmt.Fprintln(os.Stderr, "Files that exists in filesystem but are not referenced from index file (aka orphans): ")
		for _, m := range sortedKeys(inFSNotInIndex) {
			fmt.Fprintf(os.Stderr, "\t%s\n", m)
		}
		hasError = true
	}

	if hasError {
		return fmt.Errorf("orphaned files detected")
	}
	return nil
}

func missingFromFirst(first, second map[string]bool) map[string]bool {
	result := make(map[string]bool)
	for k := range second {
		if !first[k] {
			result[k] = true
		}
	}
	return result
}

func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// Simple sort
	for i := 0; i < len(keys); i++ {
		for j := i + 1; j < len(keys); j++ {
			if keys[i] > keys[j] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
	return keys
}
