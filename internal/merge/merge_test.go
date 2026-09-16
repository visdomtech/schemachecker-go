package merge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFromIndex_Basic(t *testing.T) {
	tmpDir := t.TempDir()

	// Create SQL files
	sql1 := filepath.Join(tmpDir, "001_initial.sql")
	sql2 := filepath.Join(tmpDir, "002_add_users.sql")

	if err := os.WriteFile(sql1, []byte("CREATE TABLE accounts (id INT);"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sql2, []byte("CREATE TABLE users (id INT, name TEXT);"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create index file
	indexContent := "-- Migration index\n\n001_initial.sql\n002_add_users.sql\n"
	indexFile := filepath.Join(tmpDir, "index.txt")
	if err := os.WriteFile(indexFile, []byte(indexContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// Run merge
	outputFile := filepath.Join(tmpDir, "output.sql")
	if err := FromIndex(indexFile, outputFile); err != nil {
		t.Fatalf("FromIndex failed: %v", err)
	}

	// Verify output
	data, err := os.ReadFile(outputFile)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	// Check that both files are included
	if !strings.Contains(content, "-- including 001_initial.sql") {
		t.Error("output should contain header for 001_initial.sql")
	}
	if !strings.Contains(content, "CREATE TABLE accounts") {
		t.Error("output should contain content from 001_initial.sql")
	}
	if !strings.Contains(content, "-- including 002_add_users.sql") {
		t.Error("output should contain header for 002_add_users.sql")
	}
	if !strings.Contains(content, "CREATE TABLE users") {
		t.Error("output should contain content from 002_add_users.sql")
	}

	// Check footer
	if !strings.Contains(content, "SET check_function_bodies = true;") {
		t.Error("output should contain footer")
	}

	// Check comments are preserved
	if !strings.Contains(content, "-- Migration index") {
		t.Error("output should preserve comments from index")
	}
}

func TestFromIndex_PathTraversal(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a file outside the base directory
	outsideDir := filepath.Join(tmpDir, "outside")
	if err := os.MkdirAll(outsideDir, 0o755); err != nil {
		t.Fatal(err)
	}
	outsideFile := filepath.Join(outsideDir, "secret.sql")
	if err := os.WriteFile(outsideFile, []byte("DROP TABLE users;"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create base directory with index
	baseDir := filepath.Join(tmpDir, "base")
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Index file tries to escape via path traversal
	indexContent := "../outside/secret.sql\n"
	indexFile := filepath.Join(baseDir, "index.txt")
	if err := os.WriteFile(indexFile, []byte(indexContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// Run merge - should fail
	outputFile := filepath.Join(tmpDir, "output.sql")
	err := FromIndex(indexFile, outputFile)
	if err == nil {
		t.Fatal("expected error for path traversal, got nil")
	}
	if !strings.Contains(err.Error(), "escapes base directory") {
		t.Errorf("expected 'escapes base directory' error, got: %v", err)
	}
}

func TestFromIndex_CommentsAndBlanks(t *testing.T) {
	tmpDir := t.TempDir()

	// Create SQL file
	sql1 := filepath.Join(tmpDir, "init.sql")
	if err := os.WriteFile(sql1, []byte("CREATE TABLE test (id INT);"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create index with comments and blank lines
	indexContent := "-- This is a comment\n\n-- Another comment\ninit.sql\n\n-- Final comment\n"
	indexFile := filepath.Join(tmpDir, "index.txt")
	if err := os.WriteFile(indexFile, []byte(indexContent), 0o644); err != nil {
		t.Fatal(err)
	}

	outputFile := filepath.Join(tmpDir, "output.sql")
	if err := FromIndex(indexFile, outputFile); err != nil {
		t.Fatalf("FromIndex failed: %v", err)
	}

	data, err := os.ReadFile(outputFile)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	// Verify comments are preserved
	if !strings.Contains(content, "-- This is a comment") {
		t.Error("should preserve first comment")
	}
	if !strings.Contains(content, "-- Another comment") {
		t.Error("should preserve second comment")
	}
	if !strings.Contains(content, "-- Final comment") {
		t.Error("should preserve final comment")
	}

	// Verify comment ordering: leading comments before file content, trailing after
	firstComment := strings.Index(content, "-- This is a comment")
	anotherComment := strings.Index(content, "-- Another comment")
	fileHeader := strings.Index(content, "-- including init.sql")
	finalComment := strings.Index(content, "-- Final comment")
	if firstComment > anotherComment || anotherComment > fileHeader {
		t.Error("leading comments should appear before file content")
	}
	if fileHeader > finalComment {
		t.Error("trailing comment should appear after file content")
	}

	// Verify SQL file is included
	if !strings.Contains(content, "-- including init.sql") {
		t.Error("should include init.sql")
	}
	if !strings.Contains(content, "CREATE TABLE test") {
		t.Error("should contain SQL content")
	}
}

func TestFromIndex_InterleavedComments(t *testing.T) {
	tmpDir := t.TempDir()

	// Create SQL files
	sql1 := filepath.Join(tmpDir, "users.sql")
	sql2 := filepath.Join(tmpDir, "orders.sql")
	if err := os.WriteFile(sql1, []byte("CREATE TABLE users (id INT);"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sql2, []byte("CREATE TABLE orders (id INT);"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Index with comments interleaved between file entries
	indexContent := "-- Users section\nusers.sql\n-- Orders section\norders.sql\n"
	indexFile := filepath.Join(tmpDir, "index.txt")
	if err := os.WriteFile(indexFile, []byte(indexContent), 0o644); err != nil {
		t.Fatal(err)
	}

	outputFile := filepath.Join(tmpDir, "output.sql")
	if err := FromIndex(indexFile, outputFile); err != nil {
		t.Fatalf("FromIndex failed: %v", err)
	}

	data, err := os.ReadFile(outputFile)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	// Verify ordering: "-- Users section" before users.sql content,
	// "-- Orders section" before orders.sql content
	usersComment := strings.Index(content, "-- Users section")
	usersFile := strings.Index(content, "-- including users.sql")
	ordersComment := strings.Index(content, "-- Orders section")
	ordersFile := strings.Index(content, "-- including orders.sql")

	if usersComment == -1 || usersFile == -1 || ordersComment == -1 || ordersFile == -1 {
		t.Fatal("expected all comments and file headers to be present")
	}
	if usersComment > usersFile {
		t.Error("Users section comment should appear before users.sql file header")
	}
	if usersFile > ordersComment {
		t.Error("users.sql content should appear before Orders section comment")
	}
	if ordersComment > ordersFile {
		t.Error("Orders section comment should appear before orders.sql file header")
	}
}

func TestFromIndex_MissingFile(t *testing.T) {
	tmpDir := t.TempDir()

	// Index references non-existent file
	indexContent := "nonexistent.sql\n"
	indexFile := filepath.Join(tmpDir, "index.txt")
	if err := os.WriteFile(indexFile, []byte(indexContent), 0o644); err != nil {
		t.Fatal(err)
	}

	outputFile := filepath.Join(tmpDir, "output.sql")
	err := FromIndex(indexFile, outputFile)
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
	if !strings.Contains(err.Error(), "couldn't find or read") {
		t.Errorf("expected 'couldn't find or read' error, got: %v", err)
	}
}

func TestFromIndex_EmptyIndex(t *testing.T) {
	tmpDir := t.TempDir()

	// Empty index file
	indexFile := filepath.Join(tmpDir, "index.txt")
	if err := os.WriteFile(indexFile, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	outputFile := filepath.Join(tmpDir, "output.sql")
	if err := FromIndex(indexFile, outputFile); err != nil {
		t.Fatalf("FromIndex failed on empty index: %v", err)
	}

	// Should still create output with footer
	data, err := os.ReadFile(outputFile)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	if !strings.Contains(content, "SET check_function_bodies = true;") {
		t.Error("empty index should still produce footer")
	}
}

func TestFromIndex_Subdirectory(t *testing.T) {
	tmpDir := t.TempDir()

	// Create subdirectory structure
	subDir := filepath.Join(tmpDir, "migrations", "v1")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}

	sql1 := filepath.Join(subDir, "init.sql")
	if err := os.WriteFile(sql1, []byte("CREATE TABLE v1_test (id INT);"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Index file in migrations directory
	indexContent := "v1/init.sql\n"
	indexFile := filepath.Join(tmpDir, "migrations", "index.txt")
	if err := os.WriteFile(indexFile, []byte(indexContent), 0o644); err != nil {
		t.Fatal(err)
	}

	outputFile := filepath.Join(tmpDir, "output.sql")
	if err := FromIndex(indexFile, outputFile); err != nil {
		t.Fatalf("FromIndex failed: %v", err)
	}

	data, err := os.ReadFile(outputFile)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	if !strings.Contains(content, "-- including v1/init.sql") {
		t.Error("should include subdirectory file")
	}
	if !strings.Contains(content, "CREATE TABLE v1_test") {
		t.Error("should contain SQL content from subdirectory")
	}
}

func TestFromIndex_PathTraversalAbsolute(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a file at absolute path
	outsideFile := filepath.Join(tmpDir, "outside.sql")
	if err := os.WriteFile(outsideFile, []byte("DROP TABLE accounts;"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create base directory
	baseDir := filepath.Join(tmpDir, "base")
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Index file tries to use absolute path
	// Note: filepath.Join concatenates absolute paths with base, creating invalid path
	// This test verifies that absolute paths don't escape the base directory
	indexContent := outsideFile + "\n"
	indexFile := filepath.Join(baseDir, "index.txt")
	if err := os.WriteFile(indexFile, []byte(indexContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// Run merge - should fail (either path traversal or file not found)
	outputFile := filepath.Join(tmpDir, "output.sql")
	err := FromIndex(indexFile, outputFile)
	if err == nil {
		t.Fatal("expected error for absolute path traversal, got nil")
	}
	// Absolute paths get concatenated by filepath.Join, creating invalid paths
	// So we get "couldn't find or read" instead of "escapes base directory"
	if !strings.Contains(err.Error(), "couldn't find or read") && !strings.Contains(err.Error(), "escapes base directory") {
		t.Errorf("expected file access error, got: %v", err)
	}
}

func TestFromIndex_MultiplePathTraversalAttempts(t *testing.T) {
	tmpDir := t.TempDir()

	// Create nested structure
	deepDir := filepath.Join(tmpDir, "a", "b", "c")
	if err := os.MkdirAll(deepDir, 0o755); err != nil {
		t.Fatal(err)
	}

	outsideFile := filepath.Join(tmpDir, "secret.sql")
	if err := os.WriteFile(outsideFile, []byte("DROP TABLE users;"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Index in deep directory tries to traverse up
	indexContent := "../../../secret.sql\n"
	indexFile := filepath.Join(deepDir, "index.txt")
	if err := os.WriteFile(indexFile, []byte(indexContent), 0o644); err != nil {
		t.Fatal(err)
	}

	outputFile := filepath.Join(tmpDir, "output.sql")
	err := FromIndex(indexFile, outputFile)
	if err == nil {
		t.Fatal("expected error for deep path traversal, got nil")
	}
	if !strings.Contains(err.Error(), "escapes base directory") {
		t.Errorf("expected 'escapes base directory' error, got: %v", err)
	}
}

func TestFromIndex_WhitespaceHandling(t *testing.T) {
	tmpDir := t.TempDir()

	// Create SQL file
	sql1 := filepath.Join(tmpDir, "test.sql")
	if err := os.WriteFile(sql1, []byte("CREATE TABLE test (id INT);"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Index with whitespace around filename
	indexContent := "  test.sql  \n"
	indexFile := filepath.Join(tmpDir, "index.txt")
	if err := os.WriteFile(indexFile, []byte(indexContent), 0o644); err != nil {
		t.Fatal(err)
	}

	outputFile := filepath.Join(tmpDir, "output.sql")
	if err := FromIndex(indexFile, outputFile); err != nil {
		t.Fatalf("FromIndex failed: %v", err)
	}

	data, err := os.ReadFile(outputFile)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	// Should trim whitespace and include the file
	if !strings.Contains(content, "CREATE TABLE test") {
		t.Error("should handle whitespace in index entries")
	}
}

func TestFromIndex_LargeFile(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a large SQL file
	largeContent := strings.Repeat("CREATE TABLE test (id INT);\n", 1000)
	sql1 := filepath.Join(tmpDir, "large.sql")
	if err := os.WriteFile(sql1, []byte(largeContent), 0o644); err != nil {
		t.Fatal(err)
	}

	indexContent := "large.sql\n"
	indexFile := filepath.Join(tmpDir, "index.txt")
	if err := os.WriteFile(indexFile, []byte(indexContent), 0o644); err != nil {
		t.Fatal(err)
	}

	outputFile := filepath.Join(tmpDir, "output.sql")
	if err := FromIndex(indexFile, outputFile); err != nil {
		t.Fatalf("FromIndex failed on large file: %v", err)
	}

	// Verify output exists and has reasonable size
	info, err := os.Stat(outputFile)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() < 1000 {
		t.Error("output file seems too small for large input")
	}
}

func TestFromIndex_ForeignKeysDeferred(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a subdirectory structure mimicking a schema split
	subDir := filepath.Join(tmpDir, "approval", "TABLE")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// approvers.sql has FK references to instances — listed FIRST in index
	approversSQL := `CREATE TABLE approval.approvers (
    approver_id bigserial NOT NULL,
    instance_id bigint NOT NULL
);
ALTER TABLE ONLY approval.approvers
    ADD CONSTRAINT approvers_pkey PRIMARY KEY (approver_id);

ALTER TABLE ONLY approval.approvers
    ADD CONSTRAINT approvers_instance_id_fkey FOREIGN KEY (instance_id) REFERENCES approval.instances(instance_id) ON DELETE CASCADE;`
	if err := os.WriteFile(filepath.Join(subDir, "approvers.sql"), []byte(approversSQL), 0o644); err != nil {
		t.Fatal(err)
	}

	// instances.sql — listed SECOND in index, but referenced by approvers
	instancesSQL := `CREATE TABLE approval.instances (
    instance_id bigserial NOT NULL
);
ALTER TABLE ONLY approval.instances
    ADD CONSTRAINT instances_pkey PRIMARY KEY (instance_id);

ALTER TABLE ONLY approval.instances
    ADD CONSTRAINT instances_definition_id_fkey FOREIGN KEY (definition_id) REFERENCES approval.definitions(definition_id);`
	if err := os.WriteFile(filepath.Join(subDir, "instances.sql"), []byte(instancesSQL), 0o644); err != nil {
		t.Fatal(err)
	}

	// Index lists approvers BEFORE instances
	indexContent := "approval/TABLE/approvers.sql\napproval/TABLE/instances.sql\n"
	indexFile := filepath.Join(tmpDir, "index.txt")
	if err := os.WriteFile(indexFile, []byte(indexContent), 0o644); err != nil {
		t.Fatal(err)
	}

	outputFile := filepath.Join(tmpDir, "output.sql")
	if err := FromIndex(indexFile, outputFile); err != nil {
		t.Fatalf("FromIndex failed: %v", err)
	}

	data, err := os.ReadFile(outputFile)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	// CREATE TABLE statements must come before any FOREIGN KEY constraint.
	// Find the position of the first CREATE TABLE and the first FOREIGN KEY.
	firstCreate := strings.Index(content, "CREATE TABLE")
	firstFK := strings.Index(content, "FOREIGN KEY")
	if firstCreate == -1 {
		t.Fatal("output should contain CREATE TABLE statements")
	}
	if firstFK == -1 {
		t.Fatal("output should contain FOREIGN KEY constraints")
	}
	if firstFK < firstCreate {
		t.Error("FOREIGN KEY constraint appears before CREATE TABLE — FK blocks should be deferred")
	}

	// All CREATE TABLE statements should precede all FOREIGN KEY constraints.
	lastCreateTable := strings.LastIndex(content, "CREATE TABLE")
	if lastCreateTable > firstFK {
		t.Error("a CREATE TABLE appears after the first FOREIGN KEY — all tables should precede FK constraints")
	}

	// Both tables should be present
	if !strings.Contains(content, "CREATE TABLE approval.approvers") {
		t.Error("output should contain approvers CREATE TABLE")
	}
	if !strings.Contains(content, "CREATE TABLE approval.instances") {
		t.Error("output should contain instances CREATE TABLE")
	}

	// Both FK constraints should be present (deferred, not removed)
	if !strings.Contains(content, "approvers_instance_id_fkey") {
		t.Error("output should contain approvers FK constraint")
	}
	if !strings.Contains(content, "instances_definition_id_fkey") {
		t.Error("output should contain instances FK constraint")
	}

	// Non-FK constraints (PRIMARY KEY) should be in the first pass, before FKs
	pkPos := strings.Index(content, "approvers_pkey")
	if pkPos == -1 {
		t.Fatal("output should contain approvers PRIMARY KEY")
	}
	if pkPos > firstFK {
		t.Error("PRIMARY KEY constraint should appear before FOREIGN KEY constraints")
	}
}

func TestFromIndex_NoForeignKeys(t *testing.T) {
	tmpDir := t.TempDir()

	// SQL file with no FK constraints
	sql1 := filepath.Join(tmpDir, "simple.sql")
	if err := os.WriteFile(sql1, []byte("CREATE TABLE simple (id INT);\nALTER TABLE ONLY simple\n    ADD CONSTRAINT simple_pkey PRIMARY KEY (id);"), 0o644); err != nil {
		t.Fatal(err)
	}

	indexContent := "simple.sql\n"
	indexFile := filepath.Join(tmpDir, "index.txt")
	if err := os.WriteFile(indexFile, []byte(indexContent), 0o644); err != nil {
		t.Fatal(err)
	}

	outputFile := filepath.Join(tmpDir, "output.sql")
	if err := FromIndex(indexFile, outputFile); err != nil {
		t.Fatalf("FromIndex failed: %v", err)
	}

	data, err := os.ReadFile(outputFile)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	// Should contain the normal content
	if !strings.Contains(content, "CREATE TABLE simple") {
		t.Error("output should contain CREATE TABLE")
	}
	if !strings.Contains(content, "simple_pkey") {
		t.Error("output should contain PRIMARY KEY")
	}
	// Should NOT contain the deferred FK section header
	if strings.Contains(content, "Deferred foreign key constraints") {
		t.Error("output should not contain FK section header when there are no FK constraints")
	}
}

func TestFromIndex_PreservesBlankLinesInFunctions(t *testing.T) {
	// Function bodies inside dollar-quoted strings ($$...$$) may contain
	// blank lines that are semantically part of the function. The merge
	// must preserve these blank lines so the round-trip is consistent.
	tmpDir := t.TempDir()

	funcSQL := strings.Join([]string{
		"CREATE FUNCTION orca.log_entry() RETURNS bigint",
		"    LANGUAGE plpgsql",
		"    AS $$",
		"DECLARE",
		"    new_id bigint;",
		"BEGIN",
		"    INSERT INTO orca.audit_logs (x)",
		"    VALUES (1)",
		"    RETURNING audit_id INTO new_id;",
		"",
		"    RETURN new_id;",
		"END;",
		"$$;",
	}, "\n") + "\n"

	funcFile := filepath.Join(tmpDir, "log_entry.sql")
	if err := os.WriteFile(funcFile, []byte(funcSQL), 0o644); err != nil {
		t.Fatal(err)
	}

	indexFile := filepath.Join(tmpDir, "index.txt")
	if err := os.WriteFile(indexFile, []byte("log_entry.sql\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	outFile := filepath.Join(tmpDir, "out.sql")
	if err := FromIndex(indexFile, outFile); err != nil {
		t.Fatalf("FromIndex failed: %v", err)
	}

	data, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatal(err)
	}
	output := string(data)

	// The blank line between RETURNING and RETURN must be preserved.
	if !strings.Contains(output, "RETURNING audit_id INTO new_id;\n\n    RETURN new_id;") {
		t.Errorf("blank line inside function body was lost, got:\n%s", output)
	}
}

func TestRequireFileReadable(t *testing.T) {
	tmpDir := t.TempDir()

	// Test readable file
	readableFile := filepath.Join(tmpDir, "readable.sql")
	if err := os.WriteFile(readableFile, []byte("SELECT 1;"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := requireFileReadable(readableFile, "index.txt"); err != nil {
		t.Errorf("readable file should not error: %v", err)
	}

	// Test non-existent file
	nonexistent := filepath.Join(tmpDir, "nonexistent.sql")
	if err := requireFileReadable(nonexistent, "index.txt"); err == nil {
		t.Error("non-existent file should error")
	}

	// Test directory
	dir := filepath.Join(tmpDir, "subdir")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := requireFileReadable(dir, "index.txt"); err == nil {
		t.Error("directory should error")
	}
}
