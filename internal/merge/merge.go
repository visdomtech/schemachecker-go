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

// fileBlock holds a parsed SQL file's content separated into non-FK and FK blocks,
// along with any index-level comments that preceded the file entry.
type fileBlock struct {
	line          string   // original index line (the relative path)
	headerComments []string // index comments/blanks that appeared before this entry
	nonFK         []string // non-FK statement blocks (CREATE TABLE, PK, UNIQUE, etc.)
	fkBlocks      []string // FK constraint blocks (ALTER TABLE ... FOREIGN KEY ...)
}

// FromIndex reads the index file and writes all referenced SQL files
// concatenated into the migration file.
//
// Foreign key constraints (ALTER TABLE ... ADD CONSTRAINT ... FOREIGN KEY)
// are deferred to a second pass after all other SQL. This preserves the
// ordering guarantee from pg_dump, where all CREATE TABLE statements
// precede any cross-table FK constraints, regardless of the order in
// which files are listed in the index.
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

	// pendingComments accumulates index-level comment/blank lines until
	// the next file entry claims them as its header.
	var pendingComments []string
	// trailingComments holds comment/blank lines that appear after the
	// last file entry (or when there are no file entries at all).
	var trailingComments []string
	// fileEntries collects parsed file blocks in index order.
	var fileEntries []fileBlock

	// Phase 1: parse index and read all referenced files.
	for _, line := range strings.Split(strings.TrimSuffix(content, "\n"), "\n") {
		if strings.HasPrefix(line, "--") {
			pendingComments = append(pendingComments, line)
			continue
		}
		if strings.TrimSpace(line) == "" {
			pendingComments = append(pendingComments, line)
			continue
		}

		trimmed := strings.TrimSpace(line)
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

		// Separate file content into non-FK and FK blocks.
		blocks := splitSQLBlocks(string(fileData))
		var nonFK, fk []string
		for _, block := range blocks {
			if isFKBlock(block) {
				fk = append(fk, block)
			} else {
				nonFK = append(nonFK, block)
			}
		}

		fileEntries = append(fileEntries, fileBlock{
			line:           line,
			headerComments: pendingComments,
			nonFK:          nonFK,
			fkBlocks:       fk,
		})
		pendingComments = nil
	}
	trailingComments = pendingComments

	// Phase 2: write non-FK content for each file, with associated header comments.
	for _, entry := range fileEntries {
		for _, c := range entry.headerComments {
			if _, err := fmt.Fprintln(w, c); err != nil {
				out.Close()
				return fmt.Errorf("write header comment: %w", err)
			}
		}
		if len(entry.nonFK) == 0 {
			continue
		}
		if _, err := fmt.Fprintf(w, "\n-- including %s\n", entry.line); err != nil {
			out.Close()
			return fmt.Errorf("write including header: %w", err)
		}
		for _, block := range entry.nonFK {
			if _, err := fmt.Fprintln(w, block); err != nil {
				out.Close()
				return fmt.Errorf("write file content: %w", err)
			}
		}
	}

	// Phase 3: write deferred FK constraints.
	var hasFK bool
	for _, entry := range fileEntries {
		if len(entry.fkBlocks) > 0 {
			hasFK = true
			break
		}
	}
	if hasFK {
		if _, err := fmt.Fprintln(w, "\n-- Deferred foreign key constraints"); err != nil {
			out.Close()
			return fmt.Errorf("write FK section header: %w", err)
		}
		for _, entry := range fileEntries {
			if len(entry.fkBlocks) == 0 {
				continue
			}
			if _, err := fmt.Fprintf(w, "\n-- including %s\n", entry.line); err != nil {
				out.Close()
				return fmt.Errorf("write FK including header: %w", err)
			}
			for _, block := range entry.fkBlocks {
				if _, err := fmt.Fprintln(w, block); err != nil {
					out.Close()
					return fmt.Errorf("write FK content: %w", err)
				}
			}
		}
	}

	// Write trailing index comments (comments after the last file entry).
	for _, c := range trailingComments {
		if _, err := fmt.Fprintln(w, c); err != nil {
			out.Close()
			return fmt.Errorf("write trailing comment: %w", err)
		}
	}

	// Footer
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

// splitSQLBlocks splits SQL content into statement blocks separated by blank
// lines. Each block is a contiguous run of non-blank lines joined with "\n".
func splitSQLBlocks(content string) []string {
	content = strings.TrimSuffix(content, "\n")
	lines := strings.Split(content, "\n")

	var blocks []string
	var current []string

	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			if len(current) > 0 {
				blocks = append(blocks, strings.Join(current, "\n"))
				current = nil
			}
		} else {
			current = append(current, line)
		}
	}
	if len(current) > 0 {
		blocks = append(blocks, strings.Join(current, "\n"))
	}
	return blocks
}

// isFKBlock reports whether a SQL statement block is a foreign key constraint.
// It checks for the presence of both ADD CONSTRAINT and FOREIGN KEY keywords.
func isFKBlock(block string) bool {
	return strings.Contains(block, "ADD CONSTRAINT") && strings.Contains(block, "FOREIGN KEY")
}

func requireFileReadable(path, indexFile string) error {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return fmt.Errorf("couldn't find or read the file %s referenced from the index file %s", path, indexFile)
	}
	return nil
}
