package split

import (
	"bufio"
	"errors"
	"fmt"
	"log/slog"
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
//
// Note: this operation is NOT idempotent. Re-running on a partially-merged
// directory may duplicate appended content. Always split into a fresh directory.
func MergeTableObjects(destDir string) error {
	slog.Info("merging table objects", "destDir", destDir)

	// Find all schema directories (anything containing a TABLE/ subdirectory).
	entries, err := os.ReadDir(destDir)
	if err != nil {
		return fmt.Errorf("read dest dir: %w", err)
	}

	var errs []error
	schemaCount := 0
	for _, schemaEntry := range entries {
		if !schemaEntry.IsDir() {
			continue
		}
		schemaName := schemaEntry.Name()
		tableDir := filepath.Join(destDir, schemaName, "TABLE")
		if info, err := os.Stat(tableDir); err != nil || !info.IsDir() {
			continue
		}

		schemaCount++

		// Inline standard table-owned sequences as BIGSERIAL before other merges.
		// This must run before mergeSchemaTables so that removeDefaultNextval
		// cleans DEFAULT files before they are appended to TABLE files.
		if err := inlineSequences(destDir, schemaName); err != nil {
			slog.Error("failed to inline sequences", "schema", schemaName, "error", err)
			errs = append(errs, fmt.Errorf("inline sequences for %s: %w", schemaName, err))
			continue
		}

		if err := mergeSchemaTables(destDir, schemaName, tableDir); err != nil {
			slog.Error("failed to merge schema tables", "schema", schemaName, "error", err)
			errs = append(errs, fmt.Errorf("merge %s: %w", schemaName, err))
		}
	}

	// Always run cleanup even if some schemas failed.
	removeEmptyDirs(destDir)

	if idxErr := updateIndexAfterMerge(destDir); idxErr != nil {
		errs = append(errs, fmt.Errorf("update index: %w", idxErr))
	}

	slog.Info("merge complete", "schemas_processed", schemaCount)
	return errors.Join(errs...)
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
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read %s: %w", objDir, err)
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
		return fmt.Errorf("read %s: %w", srcPath, err)
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
	if _, err := fmt.Fprintln(f); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(content); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// reOwnedBy matches ALTER SEQUENCE ... OWNED BY [schema.]table.column;
var reOwnedBy = regexp.MustCompile(`(?i)OWNED\s+BY\s+([\w."]+)`)

// reSeqNonStandard matches non-standard CREATE SEQUENCE options.
// Anchored to line start so "NO CYCLE" / "NO MINVALUE" don't false-match.
var reSeqNonStandard = regexp.MustCompile(`(?im)^\s*(CYCLE|MINVALUE\s+-?\d|MAXVALUE\s+-?\d)`)

// reIdentitySeq matches ALTER TABLE [schema.]table ALTER COLUMN col ADD GENERATED ALWAYS AS IDENTITY
var reIdentitySeq = regexp.MustCompile(`(?i)ALTER\s+TABLE\s+([\w."]+)\s+ALTER\s+COLUMN\s+(\w+)\s+ADD\s+GENERATED\s+ALWAYS\s+AS\s+IDENTITY`)

// inlineSequences scans SEQUENCE/ for table-owned standard sequences, converts
// the owning table's column to BIGSERIAL, removes the nextval DEFAULT, and
// removes the SEQUENCE file.
func inlineSequences(destDir, schemaName string) error {
	seqDir := filepath.Join(destDir, schemaName, "SEQUENCE")
	entries, err := os.ReadDir(seqDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read %s: %w", seqDir, err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		seqPath := filepath.Join(seqDir, entry.Name())
		data, err := os.ReadFile(seqPath)
		if err != nil {
			return fmt.Errorf("read %s: %w", seqPath, err)
		}
		content := string(data)

		// Only inline OWNED BY sequences (traditional bigserial pattern).
		// IDENTITY columns (GENERATED ALWAYS AS IDENTITY) are preserved
		// as-is so the round-trip matches the migrations side.
		tableName, columnName := parseSequenceOwnedBy(content)
		if tableName == "" {
			slog.Debug("skipping non-owned sequence (identity or standalone)",
				"schema", schemaName, "sequence", entry.Name())
			continue
		}

		// Only inline standard sequences (no CYCLE, no explicit MIN/MAX).
		if !isStandardSequence(content) {
			slog.Debug("skipping non-standard sequence", "schema", schemaName, "sequence", entry.Name())
			continue
		}

		// Modify the TABLE file: replace column type with bigserial.
		tablePath := filepath.Join(destDir, schemaName, "TABLE", tableName+".sql")
		found, err := inlineBigserial(tablePath, columnName)
		if err != nil {
			return err
		}
		if !found {
			slog.Warn("skipping sequence: table file not found",
				"schema", schemaName, "table", tableName, "sequence", entry.Name(),
				"expected_path", tablePath)
			continue
		}

		// Remove the nextval default from the DEFAULT file (if separate).
		defaultPath := filepath.Join(destDir, schemaName, "DEFAULT", tableName+".sql")
		if err := removeDefaultNextval(defaultPath, columnName); err != nil {
			return err
		}

		// Also remove the nextval default from the TABLE file itself,
		// since pg_dump may emit it inline after the CREATE TABLE.
		if err := removeDefaultNextval(tablePath, columnName); err != nil {
			return err
		}

		// Remove the SEQUENCE file.
		if err := os.Remove(seqPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove %s: %w", seqPath, err)
		}

		slog.Debug("inlined sequence as bigserial",
			"schema", schemaName, "table", tableName, "column", columnName)
	}
	return nil
}

// stripQuotes removes surrounding double-quotes from a PostgreSQL identifier.
func stripQuotes(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}

// splitDotRef splits a dot-separated PostgreSQL reference and strips quotes
// from each part (e.g. `"public"."users"."id"` → ["public", "users", "id"]).
func splitDotRef(ref string) []string {
	parts := strings.Split(ref, ".")
	for i := range parts {
		parts[i] = stripQuotes(parts[i])
	}
	return parts
}

// parseSequenceOwnedBy extracts table and column names from an
// ALTER SEQUENCE ... OWNED BY [schema.]table.column statement.
func parseSequenceOwnedBy(content string) (table, column string) {
	match := reOwnedBy.FindStringSubmatch(content)
	if match == nil {
		return "", ""
	}
	parts := splitDotRef(match[1])
	switch len(parts) {
	case 3: // schema.table.column
		return parts[1], parts[2]
	case 2: // table.column
		return parts[0], parts[1]
	default:
		return "", ""
	}
}

// parseIdentitySequence extracts table and column from
// ALTER TABLE [schema.]table ALTER COLUMN col ADD GENERATED ALWAYS AS IDENTITY
func parseIdentitySequence(content string) (table, column string) {
	match := reIdentitySeq.FindStringSubmatch(content)
	if match == nil {
		return "", ""
	}
	parts := splitDotRef(match[1])
	col := stripQuotes(match[2])
	switch len(parts) {
	case 2: // schema.table
		return parts[1], col
	case 1: // table
		return parts[0], col
	default:
		return "", ""
	}
}

// isStandardSequence returns true if the CREATE SEQUENCE can be expressed
// as BIGSERIAL (no CYCLE, no explicit MINVALUE/MAXVALUE).
func isStandardSequence(content string) bool {
	return !reSeqNonStandard.MatchString(content)
}

// inlineBigserial replaces the column's integer/bigint type with bigserial
// in the TABLE file. Returns (true, nil) if the table file was found and
// the column was converted, (false, nil) if the table file doesn't exist
// or the column was not found, or (false, err) on I/O failure.
func inlineBigserial(tablePath, columnName string) (found bool, err error) {
	data, err := os.ReadFile(tablePath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("read %s: %w", tablePath, err)
	}
	content := string(data)

	// Match the column definition line containing the column name.
	reCol := regexp.MustCompile(
		`(?i)(` + regexp.QuoteMeta(columnName) + `\s+)(integer|bigint|int4|int8)(\s+NOT\s+NULL)`)
	if !reCol.MatchString(content) {
		return false, nil // column not found or already serial
	}
	newContent := reCol.ReplaceAllString(content, "${1}bigserial${3}")
	if err := os.WriteFile(tablePath, []byte(newContent), 0o644); err != nil {
		return false, fmt.Errorf("write %s: %w", tablePath, err)
	}
	return true, nil
}

// removeDefaultNextval removes the ALTER COLUMN ... SET DEFAULT nextval(...) line
// for the given column from the file at filePath.
// If the file becomes empty after removal, deletes it.
func removeDefaultNextval(filePath string, columnName string) error {
	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read %s: %w", filePath, err)
	}

	// Build a column-specific regex from the pre-compiled template.
	reCol := regexp.MustCompile(
		`(?i)ALTER\s+TABLE\s+(?:ONLY\s+)?[\w."]+\s+ALTER\s+COLUMN\s+` +
			regexp.QuoteMeta(columnName) + `\s+SET\s+DEFAULT\s+nextval\([^)]*\)[^;]*;[ \t]*\n?`)
	newContent := reCol.ReplaceAllLiteralString(string(data), "")

	if len(strings.TrimSpace(newContent)) == 0 {
		if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove %s: %w", filePath, err)
		}
		return nil
	}
	return os.WriteFile(filePath, []byte(newContent), 0o644)
}

// removeEmptyDirs walks destDir bottom-up and removes any empty directories.
func removeEmptyDirs(destDir string) {
	// Collect directories bottom-up so children are visited before parents.
	var dirs []string
	filepath.Walk(destDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip inaccessible entries
		}
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
			if err := os.Remove(dir); err != nil {
				slog.Debug("could not remove empty dir", "dir", dir, "error", err)
			}
		}
	}
}

// readLines reads a file and returns its non-empty trimmed lines.
func readLines(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		if line := strings.TrimSpace(scanner.Text()); line != "" {
			lines = append(lines, line)
		}
	}
	f.Close()
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return lines, nil
}

// updateIndexAfterMerge rewrites index.txt to remove entries that no longer
// exist on disk (merged files) and keeps the rest in their original order.
func updateIndexAfterMerge(destDir string) error {
	indexPath := filepath.Join(destDir, "index.txt")
	lines, err := readLines(indexPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read index.txt: %w", err)
	}

	// Keep only entries whose files still exist.
	var kept []string
	for _, entry := range lines {
		if _, err := os.Stat(filepath.Join(destDir, entry)); err == nil {
			kept = append(kept, entry)
		}
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
