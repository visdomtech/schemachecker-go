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
// Child objects are resolved to their owning table by filename (DEFAULT,
// CONSTRAINT, FK_CONSTRAINT) or by the ON <relation> clause in the SQL (INDEX,
// TRIGGER). Objects whose owner cannot be resolved — indexes on materialized
// views, partitioned parents (ON ONLY), relations needing double-quoting,
// INSTEAD OF triggers on views, triggers on foreign tables, and DEFAULT or
// FK_CONSTRAINT files for such quoted relations — stay in their own directory.
//
// Standard OWNED BY sequences on plain integer/bigint NOT NULL columns are
// inlined as bigserial; the owned column is matched as a parsed identifier on
// its definition line, and only that one type token is rewritten.
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

// reQualifiedIdent matches a PostgreSQL qualified identifier (schema.table form),
// where each part can be unquoted [\w$]+ or double-quoted with "" escape.
const reQualifiedIdent = `(?:[\w$]+|"(?:[^"]|"")*")(?:\.(?:[\w$]+|"(?:[^"]|"")*"))*`

// reAlterTableTarget matches the leading ALTER TABLE and captures the target relation.
// Uses a simple non-greedy match up to the first whitespace — sufficient for
// extracting the relation name from pg_dump-emitted DEFAULT files.
var reAlterTableTarget = regexp.MustCompile(`(?im)^\s*ALTER\s+TABLE\s+(?:ONLY\s+)?(\S+)`)

// inlineSequences scans SEQUENCE/ for table-owned standard sequences, converts
// the owning table's column to BIGSERIAL, removes the nextval DEFAULT, and
// removes the SEQUENCE file. The operation is all-or-nothing per sequence:
// if any step fails or the stranding guard detects an unremovable nextval
// reference, the sequence is preserved and no files are modified.
func inlineSequences(destDir, schemaName string) error {
	seqDir := filepath.Join(destDir, schemaName, "SEQUENCE")
	entries, err := os.ReadDir(seqDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read %s: %w", seqDir, err)
	}

	// Scan DEFAULT/ once per schema for the stranding guard.
	defaultCache := scanDefaultDir(destDir, schemaName)

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

		// Read the TABLE file.
		tablePath := filepath.Join(destDir, schemaName, "TABLE", tableName+".sql")
		tableData, err := os.ReadFile(tablePath)
		if err != nil {
			if os.IsNotExist(err) {
				slog.Warn("skipping sequence: table file not found",
					"schema", schemaName, "table", tableName, "sequence", entry.Name(),
					"expected_path", tablePath)
				continue
			}
			return fmt.Errorf("read %s: %w", tablePath, err)
		}

		// Convert the owned column to bigserial (structured parse, no regex substitution).
		newTable, converted := bigserialColumn(string(tableData), columnName)
		if !converted {
			slog.Warn("skipping sequence: owned column is not a plain integer/bigint NOT NULL column definition",
				"schema", schemaName, "table", tableName, "column", columnName,
				"sequence", entry.Name())
			continue
		}

		// Strip the nextval default from the TABLE content.
		newTable = stripDefaultNextval(newTable, columnName)

		// Strip the nextval default from the DEFAULT file (if present).
		defaultPath := filepath.Join(destDir, schemaName, "DEFAULT", tableName+".sql")
		var newDefault string
		defaultChanged := false
		defaultIsEmpty := false
		if defData, err := os.ReadFile(defaultPath); err == nil {
			newDefault = stripDefaultNextval(string(defData), columnName)
			defaultChanged = newDefault != string(defData)
			defaultIsEmpty = len(strings.TrimSpace(newDefault)) == 0
			// Update cache so the guard sees the stripped content.
			if defaultChanged {
				key := strings.ToLower(tableName)
				for i, df := range defaultCache[key] {
					if df.path == defaultPath {
						defaultCache[key][i].content = newDefault
						break
					}
				}
			}
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("read %s: %w", defaultPath, err)
		}

		// Stranding guard: check that no nextval default for this column
		// survives in any file we cannot rewrite.
		reLeftover := reSetDefaultColumn(columnName)
		if reLeftover.MatchString(newTable) {
			slog.Warn("skipping sequence: a nextval default for the column could not be removed",
				"schema", schemaName, "table", tableName, "column", columnName,
				"sequence", entry.Name(), "location", "table_file")
			continue
		}
		// Check all DEFAULT files of this schema that target this table
		// (the cache entry for the table's own DEFAULT file was already updated above).
		stranded := false
		for _, defFile := range defaultCache[strings.ToLower(tableName)] {
			if reLeftover.MatchString(defFile.content) {
				slog.Warn("skipping sequence: a nextval default for the column could not be removed",
					"schema", schemaName, "table", tableName, "column", columnName,
					"sequence", entry.Name(), "location", defFile.path)
				stranded = true
				break
			}
		}
		if stranded {
			continue
		}

		// Atomic writes: TABLE, then DEFAULT, then remove SEQUENCE.
		if err := os.WriteFile(tablePath, []byte(newTable), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", tablePath, err)
		}
		if defaultChanged {
			if defaultIsEmpty {
				if err := os.Remove(defaultPath); err != nil && !os.IsNotExist(err) {
					return fmt.Errorf("remove %s: %w", defaultPath, err)
				}
			} else {
				if err := os.WriteFile(defaultPath, []byte(newDefault), 0o644); err != nil {
					return fmt.Errorf("write %s: %w", defaultPath, err)
				}
			}
			// Update cache so subsequent sequences see the stripped content.
			if defaultIsEmpty {
				delete(defaultCache, strings.ToLower(tableName))
			} else {
				for i, df := range defaultCache[strings.ToLower(tableName)] {
					if df.path == defaultPath {
						defaultCache[strings.ToLower(tableName)][i].content = newDefault
						break
					}
				}
			}
		}
		if err := os.Remove(seqPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove %s: %w", seqPath, err)
		}

		slog.Debug("inlined sequence as bigserial",
			"schema", schemaName, "table", tableName, "column", columnName)
	}
	return nil
}

// defaultFileEntry holds a cached DEFAULT file's path and content for the stranding guard.
type defaultFileEntry struct {
	path    string
	content string
}

// scanDefaultDir reads all DEFAULT/*.sql files for a schema and indexes them
// by the target relation name (lowercased).
func scanDefaultDir(destDir, schemaName string) map[string][]defaultFileEntry {
	defaultDir := filepath.Join(destDir, schemaName, "DEFAULT")
	entries, err := os.ReadDir(defaultDir)
	if err != nil {
		return nil
	}
	result := make(map[string][]defaultFileEntry)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		path := filepath.Join(defaultDir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		content := string(data)
		if m := reAlterTableTarget.FindStringSubmatch(content); m != nil {
			rel := m[1]
			// Use the last dot-separated part as the table name.
			parts := strings.Split(rel, ".")
			table := parts[len(parts)-1]
			result[strings.ToLower(table)] = append(result[strings.ToLower(table)], defaultFileEntry{path: path, content: content})
		}
	}
	return result
}

// readIdentToken reads the leading SQL identifier token from s.
// For quoted identifiers ("..."), it handles "" escape sequences.
// Returns the de-escaped name, whether it was quoted, the remaining string, and ok.
func readIdentToken(s string) (name string, quoted bool, rest string, ok bool) {
	s = strings.TrimLeft(s, " \t")
	if len(s) == 0 {
		return "", false, "", false
	}
	if s[0] == '"' {
		// Quoted identifier: scan until unescaped closing quote.
		var buf strings.Builder
		i := 1
		for i < len(s) {
			if s[i] == '"' {
				if i+1 < len(s) && s[i+1] == '"' {
					buf.WriteByte('"')
					i += 2
				} else {
					return buf.String(), true, s[i+1:], true
				}
			} else {
				buf.WriteByte(s[i])
				i++
			}
		}
		return "", false, "", false // unterminated quote
	}
	// Unquoted identifier: [A-Za-z0-9_$]+
	i := 0
	for i < len(s) && (s[i] == '_' || s[i] == '$' ||
		(s[i] >= 'A' && s[i] <= 'Z') ||
		(s[i] >= 'a' && s[i] <= 'z') ||
		(s[i] >= '0' && s[i] <= '9')) {
		i++
	}
	if i == 0 {
		return "", false, s, false
	}
	return s[:i], false, s[i:], true
}

// identEquals compares two PostgreSQL identifiers with proper folding semantics.
// The reference name (from OWNED BY) may have been de-quoted by parseSequenceOwnedBy,
// so we try both quoted and unquoted comparison to handle both cases.
func identEquals(aName string, aQuoted bool, bName string, bQuoted bool) bool {
	// Unquoted comparison (case-insensitive) — works when both are unquoted,
	// or when a quoted reference was de-quoted and the SQL identifier is also unquoted.
	if strings.EqualFold(aName, bName) {
		return true
	}
	// Quoted exact comparison — needed when the SQL identifier is quoted
	// and the reference was de-quoted (case must match exactly).
	if aName == bName {
		return true
	}
	return false
}

// isCreateTableHeader returns true if the trimmed line starts with CREATE TABLE (case-insensitive).
func isCreateTableHeader(trimmed string) bool {
	return len(trimmed) >= 12 && strings.EqualFold(trimmed[:12], "CREATE TABLE")
}

// isNotNullConstraint returns true if the fields represent the NOT NULL constraint.
// Accepts both "NOT NULL" (last column, no comma) and "NOT NULL," (not-last column).
func isNotNullConstraint(fields []string) bool {
	if len(fields) != 2 {
		return false
	}
	return strings.EqualFold(fields[0], "NOT") &&
		(strings.EqualFold(fields[1], "NULL") || strings.EqualFold(fields[1], "NULL,"))
}

// serialTypeTokens is the set of unquoted type names that can be inlined as bigserial.
var serialTypeTokens = map[string]bool{
	"integer": true,
	"int4":    true,
	"bigint":  true,
	"int8":    true,
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


// isStandardSequence returns true if the CREATE SEQUENCE can be expressed
// as BIGSERIAL (no CYCLE, no explicit MINVALUE/MAXVALUE).
func isStandardSequence(content string) bool {
	return !reSeqNonStandard.MatchString(content)
}

// bigserialColumn converts the named column's type to bigserial in the CREATE TABLE body.
// It uses structured line scanning with parsed identifier matching — no regex substitution.
// Returns the modified content and true if the column was converted.
func bigserialColumn(content, columnName string) (string, bool) {
	lines := strings.Split(content, "\n")

	inBody := false
	for i, line := range lines {
		trimmed := strings.TrimLeft(line, " \t")

		if !inBody {
			if isCreateTableHeader(trimmed) {
				inBody = true
			}
			continue
		}

		// End of CREATE TABLE body.
		if strings.HasPrefix(trimmed, ")") {
			break
		}

		// Read the leading column identifier.
		colName, colQuoted, rest, ok := readIdentToken(trimmed)
		if !ok {
			continue
		}
		if !identEquals(colName, colQuoted, columnName, false) {
			continue
		}

		// Read the type token (must be unquoted and in serialTypeTokens).
		typeName, typeQuoted, typeRest, ok := readIdentToken(rest)
		if !ok || typeQuoted || !serialTypeTokens[strings.ToLower(typeName)] {
			continue
		}

		// Require the tail to be exactly NOT NULL (with optional trailing comma).
		tail := strings.TrimSpace(typeRest)
		if !isNotNullConstraint(strings.Fields(tail)) {
			continue
		}

		// Compute the type token's start position from the parsed remainder.
		// rest is a literal suffix of line, so its leading whitespace offset is exact.
		typeStart := len(line) - len(strings.TrimLeft(rest, " \t"))
		lines[i] = line[:typeStart] + "bigserial" + typeRest
		return strings.Join(lines, "\n"), true
	}
	return content, false
}

// reColumnIdent builds a regex fragment matching a column identifier.
// Since parseSequenceOwnedBy strips quotes, the columnName may have been quoted
// in the original SQL. We match both quoted and unquoted forms.
func reColumnIdent(columnName string) string {
	if len(columnName) >= 2 && columnName[0] == '"' && columnName[len(columnName)-1] == '"' {
		inner := regexp.QuoteMeta(columnName[1 : len(columnName)-1])
		return `(?:"` + inner + `"|(?i:` + regexp.QuoteMeta(columnName) + `))`
	}
	escaped := regexp.QuoteMeta(columnName)
	return `(?:"` + escaped + `"|(?i:` + escaped + `))`
}

// reSetDefaultColumn builds a regex matching ALTER TABLE ... ALTER COLUMN <col> SET DEFAULT nextval(...);
// for the given column, with quote-aware relation and column matching.
func reSetDefaultColumn(columnName string) *regexp.Regexp {
	// Match a single ALTER TABLE ... ALTER COLUMN <col> SET DEFAULT nextval(...);
	// statement. The relation between ALTER TABLE and ALTER COLUMN is matched
	// lazily (\S.*?) rather than with a precise qualified-identifier grammar,
	// because Go's RE2 does not support lookaheads or atomic groups.
	// [^;\n]* ensures the match stays within a single SQL statement.
	return regexp.MustCompile(`(?i)ALTER[ \t]+TABLE[ \t]+(?:ONLY[ \t]+)?\S.*?[ \t]+ALTER[ \t]+COLUMN[ \t]+` +
		reColumnIdent(columnName) + `[ \t]+SET[ \t]+DEFAULT[ \t]+nextval\([^)]*\)[^;\n]*;[ \t]*\n?`)
}

// stripDefaultNextval removes ALTER TABLE ... ALTER COLUMN <col> SET DEFAULT nextval(...)
// statements from content for the specified column. Pure function — no file I/O.
func stripDefaultNextval(content, columnName string) string {
	re := reSetDefaultColumn(columnName)
	return re.ReplaceAllLiteralString(content, "")
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
