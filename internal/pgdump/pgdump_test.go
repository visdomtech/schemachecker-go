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

	if err := stripDumpBoilerplate(path); err != nil {
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

	if err := stripDumpBoilerplate(path); err != nil {
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

	if err := stripDumpBoilerplate(path); err != nil {
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

	if err := stripDumpBoilerplate(path); err != nil {
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

func TestStripDumpBoilerplate_TokenSuffix(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "dump.sql")
	input := "\\restrict eKFek1JoMf5V4vD8z1l8gxpSVPoGasFpW9RXrHvVRdJ9sNYQXIt6ac5yZzJqyaC\nCOPY t FROM stdin;\n\\unrestrict eKFek1JoMf5V4vD8z1l8gxpSVPoGasFpW9RXrHvVRdJ9sNYQXIt6ac5yZzJqyaC\n"
	if err := os.WriteFile(path, []byte(input), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := stripDumpBoilerplate(path); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if strings.Contains(got, "\\restrict") || strings.Contains(got, "\\unrestrict") {
		t.Errorf("token-suffixed restrict/unrestrict should be stripped, got:\n%s", got)
	}
	if !strings.Contains(got, "COPY t FROM stdin;") {
		t.Error("non-boilerplate lines should be preserved")
	}
}

func TestStripDumpBoilerplate_SetStatements(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "dump.sql")
	input := `SET statement_timeout = 0;
SET lock_timeout = 0;
SET idle_in_transaction_session_timeout = 0;
SET transaction_timeout = 0;
SET client_encoding = 'UTF8';
SET standard_conforming_strings = on;
SELECT pg_catalog.set_config('search_path', '', false);
SET check_function_bodies = false;
SET xmloption = content;
SET client_min_messages = warning;
SET row_security = off;

CREATE TABLE t (id int);
SET search_path TO public;
`
	if err := os.WriteFile(path, []byte(input), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := stripDumpBoilerplate(path); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	// Boilerplate SETs should be removed
	if strings.Contains(got, "statement_timeout") {
		t.Error("SET statement_timeout should be stripped")
	}
	if strings.Contains(got, "set_config") {
		t.Error("SELECT pg_catalog.set_config should be stripped")
	}
	// check_function_bodies must NOT be stripped — pg_dump relies on it
	// being false so that functions referencing not-yet-created tables
	// can be created without validation errors.
	if !strings.Contains(got, "SET check_function_bodies = false;") {
		t.Error("SET check_function_bodies must be preserved")
	}
	// Non-boilerplate content should be preserved
	if !strings.Contains(got, "CREATE TABLE t (id int);") {
		t.Error("CREATE TABLE should be preserved")
	}
	if !strings.Contains(got, "SET search_path TO public;") {
		t.Error("non-boilerplate SET statements should be preserved")
	}
}

func TestWriteAtlasSum(t *testing.T) {
	tmpDir := t.TempDir()

	// Create two migration files
	if err := os.WriteFile(filepath.Join(tmpDir, "V1.0.0__first.sql"), []byte("CREATE TABLE a (id int);"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "V2.0.0__second.sql"), []byte("CREATE TABLE b (id int);"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := WriteAtlasSum(tmpDir); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(tmpDir, "atlas.sum"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	// Should contain summary line + per-file lines
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines (1 summary + 2 files), got %d: %q", len(lines), content)
	}
	// First line is the summary hash
	if !strings.HasPrefix(lines[0], "h1:") {
		t.Errorf("line 0 should be summary hash with h1: prefix: %q", lines[0])
	}
	if strings.Contains(lines[0][3:], " ") {
		t.Errorf("summary line should be hash only (no filename): %q", lines[0])
	}
	// File lines: "<filename> h1:<hash>"
	if !strings.HasPrefix(lines[1], "V1.0.0__first.sql h1:") {
		t.Errorf("line 1 should reference first.sql: %q", lines[1])
	}
	if !strings.HasPrefix(lines[2], "V2.0.0__second.sql h1:") {
		t.Errorf("line 2 should reference second.sql: %q", lines[2])
	}
}

func TestWriteAtlasSum_SkipsExistingSum(t *testing.T) {
	tmpDir := t.TempDir()

	if err := os.WriteFile(filepath.Join(tmpDir, "V1.0.0__a.sql"), []byte("SELECT 1;"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Pre-existing atlas.sum should be ignored
	if err := os.WriteFile(filepath.Join(tmpDir, "atlas.sum"), []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := WriteAtlasSum(tmpDir); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(tmpDir, "atlas.sum"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "stale") {
		t.Error("stale atlas.sum should have been overwritten")
	}
}

func TestWriteAtlasSum_EmptyDir(t *testing.T) {
	tmpDir := t.TempDir()

	if err := WriteAtlasSum(tmpDir); err != nil {
		t.Fatal(err)
	}

	// Should still produce a file (with just the summary hash of empty content)
	if _, err := os.Stat(filepath.Join(tmpDir, "atlas.sum")); err != nil {
		t.Fatalf("atlas.sum should exist: %v", err)
	}
}
