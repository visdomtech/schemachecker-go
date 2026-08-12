package split

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// mergeableTypes lists the object type directories whose contents should be
// merged into the corresponding TABLE SQL file by matching filename to table name.
var mergeableTypes = []string{"DEFAULT", "CONSTRAINT", "FK_CONSTRAINT"}

// contentMergeTypes lists object types whose files are matched to tables by
// scanning SQL content (because the split may name files after the object
// rather than the table).
var contentMergeTypes = []string{"INDEX", "TRIGGER"}

// MergeTableObjects post-processes a split dump directory: for each table, it
// appends the SQL from INDEX, TRIGGER, DEFAULT, CONSTRAINT, and FK_CONSTRAINT
// files into the table's TABLE/*.sql file, then removes the now-empty
// directories.
func MergeTableObjects(destDir string) error {
	// Find all schema directories (anything containing a TABLE/ subdirectory).
	entries, err := os.ReadDir(destDir)
	if err != nil {
		return fmt.Errorf("read dest dir: %w", err)
	}

	for _, schemaEntry := range entries {
		if !schemaEntry.IsDir() {
			continue
		}
		schemaName := schemaEntry.Name()
		tableDir := filepath.Join(destDir, schemaName, "TABLE")
		if info, err := os.Stat(tableDir); err != nil || !info.IsDir() {
			continue
		}

		if err := mergeSchemaTables(destDir, schemaName, tableDir); err != nil {
			return err
		}
	}

	// Remove directories left empty after merging.
	removeEmptyDirs(destDir)

	// Update index.txt to reflect merged files.
	return updateIndexAfterMerge(destDir)
}

// mergeSchemaTables merges table-associated objects for all tables in one schema.
func mergeSchemaTables(destDir, schemaName, tableDir string) error {
	tableEntries, err := os.ReadDir(tableDir)
	if err != nil {
		return fmt.Errorf("read TABLE dir: %w", err)
	}

	for _, tableEntry := range tableEntries {
		if tableEntry.IsDir() || !strings.HasSuffix(tableEntry.Name(), ".sql") {
			continue
		}
		tableName := strings.TrimSuffix(tableEntry.Name(), ".sql")
		tablePath := filepath.Join(tableDir, tableEntry.Name())

		for _, objType := range mergeableTypes {
			objFile := filepath.Join(destDir, schemaName, objType, tableName+".sql")
			if err := appendIfExists(tablePath, objFile); err != nil {
				return fmt.Errorf("merge %s for %s.%s: %w", objType, schemaName, tableName, err)
			}
		}

		// INDEX and TRIGGER files may be named after the object (index name,
		// trigger name) rather than the table. Scan their content to find
		// which table they belong to.
		for _, objType := range contentMergeTypes {
			objDir := filepath.Join(destDir, schemaName, objType)
			if err := mergeByContent(tablePath, objDir, schemaName, tableName); err != nil {
				return err
			}
		}
	}
	return nil
}

// reOnTable matches ON [schema.]table in CREATE INDEX and CREATE TRIGGER statements.
var reOnTable = regexp.MustCompile(`(?i)ON\s+([\w.]+)\s*[\s(;,]`)

// mergeByContent scans the object directory for SQL files whose content
// references the given table (via an ON clause), appends them to the table
// file, and removes them.
func mergeByContent(tablePath, objDir, schemaName, tableName string) error {
	entries, err := os.ReadDir(objDir)
	if err != nil {
		return nil // directory may not exist
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		objPath := filepath.Join(objDir, entry.Name())
		data, err := os.ReadFile(objPath)
		if err != nil {
			return fmt.Errorf("read %s: %w", objPath, err)
		}
		if referencesTable(string(data), schemaName, tableName) {
			if err := appendContent(tablePath, data); err != nil {
				return err
			}
			if err := os.Remove(objPath); err != nil {
				return fmt.Errorf("remove merged file: %w", err)
			}
		}
	}
	return nil
}

// referencesTable returns true if the SQL content references the given table
// via an ON clause (CREATE INDEX ... ON table, CREATE TRIGGER ... ON table).
func referencesTable(content, schemaName, tableName string) bool {
	match := reOnTable.FindStringSubmatch(content)
	if match == nil {
		return false
	}
	ref := match[1]
	return ref == tableName || ref == schemaName+"."+tableName
}

// appendIfExists reads src and appends it to dst if src exists.
// Removes src after a successful append.
func appendIfExists(dstPath, srcPath string) error {
	data, err := os.ReadFile(srcPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return os.Remove(srcPath)
	}
	if err := appendContent(dstPath, data); err != nil {
		return err
	}
	return os.Remove(srcPath)
}

// appendContent appends content to dstPath with a blank-line separator.
func appendContent(dstPath string, content []byte) error {
	f, err := os.OpenFile(dstPath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open table file: %w", err)
	}
	defer f.Close()

	// Blank line separator
	if _, err := fmt.Fprintln(f); err != nil {
		return err
	}
	_, err = f.Write(content)
	return err
}

// removeEmptyDirs walks destDir bottom-up and removes any empty directories.
func removeEmptyDirs(destDir string) {
	// Collect directories bottom-up so children are visited before parents.
	var dirs []string
	filepath.Walk(destDir, func(path string, info os.FileInfo, _ error) error {
		if info.IsDir() && path != destDir {
			dirs = append(dirs, path)
		}
		return nil
	})
	// Reverse for bottom-up removal.
	for i, j := 0, len(dirs)-1; i < j; i, j = i+1, j-1 {
		dirs[i], dirs[j] = dirs[j], dirs[i]
	}
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err == nil && len(entries) == 0 {
			os.Remove(dir)
		}
	}
}

// updateIndexAfterMerge rewrites index.txt to remove entries that no longer
// exist on disk (merged files) and keeps the rest in their original order.
func updateIndexAfterMerge(destDir string) error {
	indexPath := filepath.Join(destDir, "index.txt")
	f, err := os.Open(indexPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	var kept []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		entry := strings.TrimSpace(line)
		if entry == "" {
			continue
		}
		fullPath := filepath.Join(destDir, entry)
		if _, err := os.Stat(fullPath); err == nil {
			kept = append(kept, entry)
		}
	}
	f.Close()
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read index.txt: %w", err)
	}

	// Rewrite index.txt with surviving entries.
	out, err := os.Create(indexPath)
	if err != nil {
		return fmt.Errorf("rewrite index.txt: %w", err)
	}
	for _, entry := range kept {
		if _, err := fmt.Fprintln(out, entry); err != nil {
			out.Close()
			return err
		}
	}
	return out.Close()
}
