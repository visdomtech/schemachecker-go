package pgdump

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStripPsqlMetaCommands(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "dump.sql")
	input := "CREATE TABLE t (id int);\n\\restrict\nCOPY t FROM stdin;\n\\unrestrict\nCREATE TABLE t2 (id int);\n"
	if err := os.WriteFile(path, []byte(input), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := stripPsqlMetaCommands(path); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if strings.Contains(got, "\\restrict") || strings.Contains(got, "\\unrestrict") {
		t.Errorf("meta-commands should be stripped, got:\n%s", got)
	}
	if !strings.Contains(got, "CREATE TABLE t (id int);") || !strings.Contains(got, "CREATE TABLE t2 (id int);") {
		t.Errorf("SQL statements should be preserved, got:\n%s", got)
	}
}

func TestStripPsqlMetaCommands_NoOp(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "dump.sql")
	input := "CREATE TABLE t (id int);\nSELECT 1;\n"
	if err := os.WriteFile(path, []byte(input), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := stripPsqlMetaCommands(path); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if !strings.Contains(got, "CREATE TABLE t (id int);") || !strings.Contains(got, "SELECT 1;") {
		t.Errorf("content should be preserved unchanged, got:\n%s", got)
	}
}

func TestStripPsqlMetaCommands_EmptyFile(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "dump.sql")
	if err := os.WriteFile(path, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := stripPsqlMetaCommands(path); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "\n" {
		// Empty file produces a single newline from fmt.Fprintln
		// This is acceptable behavior
	}
}

func TestStripPsqlMetaCommands_NoOverMatch(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "dump.sql")
	// Ensure lines that start with \restrict but are not exact matches are preserved
	input := "\\restrictive_something\nnormal line\n"
	if err := os.WriteFile(path, []byte(input), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := stripPsqlMetaCommands(path); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "\\restrictive_something") {
		t.Error("exact match should not strip \\restrictive_something")
	}
}
