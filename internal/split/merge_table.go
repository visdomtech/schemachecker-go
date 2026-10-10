package split

import (
	"bufio"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
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
		// This must run before mergeSchemaTables so that stripDefaultNextval
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

// childFile holds a child object file's path and content during classification.
type childFile struct {
	path    string
	content []byte
}

// mergeSchemaTables merges table-associated objects for all tables in one schema.
// It classifies child files in a single pass per directory, avoiding the
// O(tables × child-files) re-scan of the original per-table loop.
func mergeSchemaTables(destDir, schemaName, tableDir string) error {
	tableEntries, err := os.ReadDir(tableDir)
	if err != nil {
		return fmt.Errorf("read TABLE dir: %w", err)
	}

	// Build a set of known table names for content-based classification.
	tableNames := make(map[string]bool)
	for _, entry := range tableEntries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		tableName := strings.TrimSuffix(entry.Name(), ".sql")
		tableNames[tableName] = true
	}

	// --- Single classification pass for filename-based types (DEFAULT, CONSTRAINT, FK_CONSTRAINT) ---
	type tableFiles map[string][]childFile // tableName → files
	mergeableMaps := make(map[string]tableFiles)
	for _, objType := range mergeableTypes {
		mergeableMaps[objType] = make(tableFiles)
		objDir := filepath.Join(destDir, schemaName, objType)
		entries, err := os.ReadDir(objDir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return fmt.Errorf("read %s dir: %w", objType, err)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
				continue
			}
			tableName := strings.TrimSuffix(entry.Name(), ".sql")
			objPath := filepath.Join(objDir, entry.Name())
			data, err := os.ReadFile(objPath)
			if err != nil {
				return fmt.Errorf("read %s: %w", objPath, err)
			}
			if len(strings.TrimSpace(string(data))) == 0 {
				_ = os.Remove(objPath)
				continue
			}
			mergeableMaps[objType][tableName] = append(mergeableMaps[objType][tableName], childFile{path: objPath, content: data})
		}
	}

	// --- Single classification pass for content-based types (INDEX, TRIGGER) ---
	contentMaps := make(map[string]tableFiles)
	for _, objType := range contentMergeTypes {
		contentMaps[objType] = make(tableFiles)
		objDir := filepath.Join(destDir, schemaName, objType)
		entries, err := os.ReadDir(objDir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return fmt.Errorf("read %s dir: %w", objType, err)
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
			tableName, ok := tableForContent(string(data), schemaName, tableNames)
			if !ok {
				continue
			}
			contentMaps[objType][tableName] = append(contentMaps[objType][tableName], childFile{path: objPath, content: data})
		}
	}

	// --- Per-table append: look up classified files and append in order ---
	for _, tableEntry := range tableEntries {
		if tableEntry.IsDir() || !strings.HasSuffix(tableEntry.Name(), ".sql") {
			continue
		}
		tableName := strings.TrimSuffix(tableEntry.Name(), ".sql")
		tablePath := filepath.Join(tableDir, tableEntry.Name())

		// Filename-based types in order: DEFAULT, CONSTRAINT, FK_CONSTRAINT
		for _, objType := range mergeableTypes {
			for _, cf := range mergeableMaps[objType][tableName] {
				if err := appendContent(tablePath, cf.content); err != nil {
					return fmt.Errorf("merge %s for %s.%s: %w", objType, schemaName, tableName, err)
				}
				if err := os.Remove(cf.path); err != nil {
					return fmt.Errorf("remove merged file: %w", err)
				}
			}
		}

		// Content-based types in order: INDEX, TRIGGER
		for _, objType := range contentMergeTypes {
			for _, cf := range contentMaps[objType][tableName] {
				if err := appendContent(tablePath, cf.content); err != nil {
					return fmt.Errorf("merge %s for %s.%s: %w", objType, schemaName, tableName, err)
				}
				if err := os.Remove(cf.path); err != nil {
					return fmt.Errorf("remove merged file: %w", err)
				}
			}
		}
	}

	return nil
}

// reOnTable matches ON [schema.]table in CREATE INDEX and CREATE TRIGGER statements.
var reOnTable = regexp.MustCompile(`(?i)ON\s+([\w.]+)\s*[\s(;,]`)

// tableForContent extracts the referenced table name from SQL content using the
// first ON clause. It returns the table name and true if the reference matches
// a known table in the given schema.
func tableForContent(content, schemaName string, tableNames map[string]bool) (string, bool) {
	match := reOnTable.FindStringSubmatch(content)
	if match == nil {
		return "", false
	}
	ref := match[1]
	if tableNames[ref] {
		return ref, true
	}
	if strings.Contains(ref, ".") {
		parts := strings.SplitN(ref, ".", 2)
		if parts[0] == schemaName && tableNames[parts[1]] {
			return parts[1], true
		}
	}
	return "", false
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
var reAlterTableTarget = regexp.MustCompile(`(?im)^\s*ALTER\s+TABLE\s+(?:ONLY\s+)?(` + reQualifiedIdent + `)`)

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

		// Compile the nextval regex once per sequence and reuse for strip + guard.
		reNextval := reSetDefaultColumn(columnName)

		// Strip the nextval default from the TABLE content.
		newTable = reNextval.ReplaceAllLiteralString(newTable, "")

		// Strip the nextval default from the DEFAULT file (if present).
		defaultPath := filepath.Join(destDir, schemaName, "DEFAULT", tableName+".sql")
		var newDefault string
		defaultChanged := false
		defaultIsEmpty := false
		if defData, err := os.ReadFile(defaultPath); err == nil {
			newDefault = reNextval.ReplaceAllLiteralString(string(defData), "")
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
		if reNextval.MatchString(newTable) {
			slog.Warn("skipping sequence: a nextval default for the column could not be removed",
				"schema", schemaName, "table", tableName, "column", columnName,
				"sequence", entry.Name(), "location", "table_file")
			continue
		}
		// Check all DEFAULT files of this schema that target this table
		// (the cache entry for the table's own DEFAULT file was already updated above).
		stranded := false
		for _, defFile := range defaultCache[strings.ToLower(tableName)] {
			if reNextval.MatchString(defFile.content) {
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
		if !os.IsNotExist(err) {
			slog.Warn("could not scan DEFAULT directory for stranding guard",
				"schema", schemaName, "error", err)
		}
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
			slog.Warn("could not read DEFAULT file for stranding guard",
				"schema", schemaName, "file", entry.Name(), "error", err)
			continue
		}
		content := string(data)
		if m := reAlterTableTarget.FindStringSubmatch(content); m != nil {
			rel := m[1]
			// Use the last dot-separated part as the table name, stripping
			// surrounding quotes so the cache key matches the de-quoted
			// tableName produced by parseSequenceOwnedBy.
			parts := splitDotRef(rel)
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
func identEquals(aName string, _ bool, bName string, _ bool) bool {
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

// isCreateTableHeader returns true if the trimmed line starts with CREATE
// [UNLOGGED|TEMPORARY|TEMP] TABLE (case-insensitive).
func isCreateTableHeader(trimmed string) bool {
	if len(trimmed) < 7 || !strings.EqualFold(trimmed[:7], "CREATE ") {
		return false
	}
	rest := strings.TrimLeft(trimmed[7:], " \t")
	for _, mod := range []string{"UNLOGGED ", "TEMPORARY ", "TEMP "} {
		if len(rest) >= len(mod) && strings.EqualFold(rest[:len(mod)], mod) {
			rest = strings.TrimLeft(rest[len(mod):], " \t")
			break
		}
	}
	return len(rest) >= 5 && strings.EqualFold(rest[:5], "TABLE")
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

// reNextvalRef matches nextval('...') to extract the sequence reference name.
var reNextvalRef = regexp.MustCompile(`nextval\('([^']+)'`)

// updateIndexAfterMerge rewrites index.txt to remove entries that no longer
// exist on disk (merged files), then reorders referenced SEQUENCE entries to
// appear before their referencing TABLE entries (N1 fix).
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

	// N1: reorder so referenced SEQUENCE entries precede their TABLEs.
	kept = reorderSequenceBeforeTable(destDir, kept)

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

// reorderSequenceBeforeTable hoists SEQUENCE entries in front of the earliest
// TABLE that references them via nextval('...'). Unreferenced sequences keep
// their original positions. The reorder is stable: relative order of all other
// entries is preserved.
func reorderSequenceBeforeTable(destDir string, entries []string) []string {
	// Map sequence name (filename stem) → original index.
	seqEntries := make(map[string]int)
	for i, entry := range entries {
		if strings.Contains(entry, "/SEQUENCE/") {
			name := strings.TrimSuffix(filepath.Base(entry), ".sql")
			seqEntries[name] = i
		}
	}
	if len(seqEntries) == 0 {
		return entries
	}
	// For each TABLE, find referenced sequences via nextval('...').
	// seqName → earliest table index that references it.
	seqToEarliestTable := make(map[string]int)
	for i, entry := range entries {
		if !strings.Contains(entry, "/TABLE/") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(destDir, entry))
		if err != nil {
			continue
		}
		for _, match := range reNextvalRef.FindAllStringSubmatch(string(data), -1) {
			seqRef := match[1]
			seqName := stripQuotes(seqRef)
			if strings.Contains(seqName, ".") {
				parts := strings.Split(seqName, ".")
				seqName = parts[len(parts)-1]
			}
			if _, ok := seqEntries[seqName]; !ok {
				continue
			}
			if current, ok := seqToEarliestTable[seqName]; !ok || i < current {
				seqToEarliestTable[seqName] = i
			}
		}
	}

	type move struct {
		seqName   string
		seqIdx    int
		targetIdx int
	}
	var moves []move
	for seqName, seqIdx := range seqEntries {
		tableIdx, ok := seqToEarliestTable[seqName]
		if !ok || seqIdx <= tableIdx {
			continue
		}
		moves = append(moves, move{seqName: seqName, seqIdx: seqIdx, targetIdx: tableIdx})
	}

	if len(moves) == 0 {
		return entries
	}

	// Sort moves by targetIdx, then by original seqIdx (stable).
	sort.Slice(moves, func(i, j int) bool {
		if moves[i].targetIdx != moves[j].targetIdx {
			return moves[i].targetIdx < moves[j].targetIdx
		}
		return moves[i].seqIdx < moves[j].seqIdx
	})

	// Build insertion map: targetIdx → []seqIdx
	insertions := make(map[int][]int)
	seqMoved := make(map[int]bool)
	for _, m := range moves {
		insertions[m.targetIdx] = append(insertions[m.targetIdx], m.seqIdx)
		seqMoved[m.seqIdx] = true
	}

	// Build the reordered list.
	var result []string
	for i, entry := range entries {
		// Insert sequences that should appear before this entry.
		for _, seqIdx := range insertions[i] {
			result = append(result, entries[seqIdx])
		}
		// Skip sequences at their original positions if they've been moved.
		if seqMoved[i] {
			continue
		}
		result = append(result, entry)
	}

	return result
}
