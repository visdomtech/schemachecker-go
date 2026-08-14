// Package pgdump executes pg_dump against a provisioned PostgreSQL database.
package pgdump

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/caarlos0/env/v11"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/visdomtech/orcacommon/postgres"
)

// Options configures the pg_dump invocation.
type Options struct {
	// SchemaOnly dumps only the schema (no data).
	SchemaOnly bool
	// OutputFile is the path to write the dump to.
	OutputFile string
}

// DumpFromPool runs pg_dump against the database connected via the given pool.
// pg_dump output is streamed directly to the output file to minimize peak memory.
func DumpFromPool(ctx context.Context, pool *pgxpool.Pool, opts Options) error {
	cfg := pool.Config().ConnConfig
	host := cfg.Host
	port := fmt.Sprintf("%d", cfg.Port)
	user := cfg.User
	password := cfg.Password
	dbname := cfg.Database

	args := []string{"pg_dump"}

	if opts.SchemaOnly {
		args = append(args, "--schema-only")
	}

	// The split can handle privileges, but it's not needed for our usecase
	args = append(args, "--no-privileges")

	// We don't need owner statements in the dump
	args = append(args, "--no-owner")

	// We never want flyway_schema_history / atlas_schema_revisions which is outside of our control
	args = append(args, "--exclude-table=flyway_schema_history")
	args = append(args, "--exclude-table=atlas_schema_revisions")

	args = append(args, fmt.Sprintf("--username=%s", user))
	args = append(args, fmt.Sprintf("--role=%s", user))
	args = append(args, fmt.Sprintf("--host=%s", host))
	args = append(args, fmt.Sprintf("--port=%s", port))
	args = append(args, dbname)

	slog.Debug("running pg_dump", "host", host, "port", port, "db", dbname)

	outFile, err := os.Create(opts.OutputFile)
	if err != nil {
		return fmt.Errorf("create dump output file: %w", err)
	}

	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	// Use minimal environment to avoid leaking parent secrets to child process.
	cmd.Env = []string{
		fmt.Sprintf("PGPASSWORD=%s", password),
		"PATH=" + os.Getenv("PATH"),
	}
	if home := os.Getenv("HOME"); home != "" {
		cmd.Env = append(cmd.Env, "HOME="+home)
	}
	if tz := os.Getenv("TZ"); tz != "" {
		cmd.Env = append(cmd.Env, "TZ="+tz)
	}
	cmd.Stdout = outFile

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		outFile.Close()
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return fmt.Errorf("pg_dump failed: %s\n%s", err, stderr.String())
		}
		return fmt.Errorf("pg_dump failed: %w", err)
	}

	if err := outFile.Close(); err != nil {
		return fmt.Errorf("close dump output file: %w", err)
	}

	if err := stripDumpBoilerplate(opts.OutputFile); err != nil {
		return fmt.Errorf("strip dump boilerplate: %w", err)
	}

	return nil
}

// stripDumpBoilerplate removes pg_dump boilerplate that is not part of the
// schema definition:
//   - \restrict / \unrestrict psql meta-commands (with optional token suffix)
//   - Standard SET statements emitted at the top of every dump
//   - The SELECT pg_catalog.set_config('search_path', ...) line
func stripDumpBoilerplate(path string) error {
	in, err := os.Open(path)
	if err != nil {
		return err
	}
	defer in.Close()

	tmp, err := os.CreateTemp(filepath.Dir(path), ".pgdump-strip-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()

	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)
	writer := bufio.NewWriter(tmp)
	for scanner.Scan() {
		line := scanner.Text()
		if shouldStripLine(line) {
			continue
		}
		if _, err := fmt.Fprintln(writer, line); err != nil {
			tmp.Close()
			os.Remove(tmpName)
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := writer.Flush(); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}

	return os.Rename(tmpName, path)
}

// shouldStripLine reports whether a pg_dump output line should be removed.
func shouldStripLine(line string) bool {
	// \restrict / \unrestrict psql meta-commands (may have a token suffix)
	// Use exact match or prefix-with-space to avoid matching e.g. \restrictive
	if line == `\restrict` || strings.HasPrefix(line, `\restrict `) ||
		line == `\unrestrict` || strings.HasPrefix(line, `\unrestrict `) {
		return true
	}
	return dumpBoilerplate[line]
}

// dumpBoilerplate contains the exact pg_dump SET/SELECT lines that are emitted
// at the top of every dump and carry no schema information.
// NOTE: "SET check_function_bodies = false;" is intentionally NOT stripped —
// pg_dump places function definitions before the tables they reference, and
// this setting is required so PostgreSQL skips function body validation at
// creation time.
var dumpBoilerplate = map[string]bool{
	"SET statement_timeout = 0;":                              true,
	"SET lock_timeout = 0;":                                   true,
	"SET idle_in_transaction_session_timeout = 0;":            true,
	"SET transaction_timeout = 0;":                            true,
	"SET client_encoding = 'UTF8';":                           true,
	"SET standard_conforming_strings = on;":                   true,
	"SELECT pg_catalog.set_config('search_path', '', false);": true,
	"SET xmloption = content;":                                true,
	"SET client_min_messages = warning;":                      true,
	"SET row_security = off;":                                 true,
}

// ProvisionAndDump creates a testcontainer PostgreSQL, runs migrations from
// migrationDir, and produces a pg_dump to outputFile.
func ProvisionAndDump(ctx context.Context, migrationDir, outputFile string, schemaOnly bool) error {
	if strings.TrimSpace(migrationDir) == "" {
		return fmt.Errorf("migrationDir must not be empty")
	}
	key := fmt.Sprintf("schemachecker-%s", sanitizeKey(migrationDir))

	var dbcfg postgres.DBConfig
	if err := env.ParseWithOptions(&dbcfg, env.Options{Prefix: "DB_"}); err != nil {
		return fmt.Errorf("parse database config from environment: %w", err)
	}
	migrator := postgres.NewMigrator(os.DirFS(migrationDir), nil)

	pool, err := postgres.OpenPoolWithKey(ctx, dbcfg, migrator, key)
	if err != nil {
		return fmt.Errorf("provision database: %w", err)
	}
	// Note: pool is cached by orcacommon; do NOT close it here.
	// It will be cleaned up on process shutdown.

	return DumpFromPool(ctx, pool, Options{
		SchemaOnly: schemaOnly,
		OutputFile: outputFile,
	})
}

// WriteAtlasSum generates an atlas.sum checksum file in dir.
// Atlas requires this file to validate the integrity of the migration directory.
//
// The hash algorithm (from ariga.io/atlas/sql/migrate):
//   - Per-file hashes are cumulative: each file's hash includes all previous
//     filenames + contents plus its own, fed into a rolling SHA-256.
//   - The summary hash is SHA-256 over the concatenation of all (name + hash) pairs.
//
// Output format:
//
//	Line 1:       h1:<base64-summary>          (summary)
//	Lines 2..N:   <filename> h1:<base64-hash>  (per-file cumulative hash)
func WriteAtlasSum(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read migration directory: %w", err)
	}

	// Collect only .sql files (Atlas only considers *.sql), skip atlas.sum
	var sqlFiles []string
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".sql" {
			continue
		}
		sqlFiles = append(sqlFiles, e.Name())
	}
	sort.Strings(sqlFiles)

	// Build cumulative per-file hashes (matching Atlas NewHashFile)
	type entry struct{ name, hash string }
	var fileEntries []entry
	h := sha256.New()
	for _, name := range sqlFiles {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return fmt.Errorf("read migration file %s: %w", name, err)
		}
		h.Write([]byte(name))
		h.Write(data)
		fileEntries = append(fileEntries, entry{name, base64.StdEncoding.EncodeToString(h.Sum(nil))})
	}

	// Summary: SHA-256 over concatenation of all (name + hash) pairs
	sum := sha256.New()
	for _, e := range fileEntries {
		sum.Write([]byte(e.name))
		sum.Write([]byte(e.hash))
	}

	var out strings.Builder
	fmt.Fprintf(&out, "h1:%s\n", base64.StdEncoding.EncodeToString(sum.Sum(nil)))
	for _, e := range fileEntries {
		fmt.Fprintf(&out, "%s h1:%s\n", e.name, e.hash)
	}

	return os.WriteFile(filepath.Join(dir, "atlas.sum"), []byte(out.String()), 0o644)
}

func sanitizeKey(s string) string {
	r := strings.NewReplacer("/", "-", "\\", "-", " ", "-", ":", "-")
	return r.Replace(s)
}
