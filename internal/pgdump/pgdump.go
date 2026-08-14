// Package pgdump executes pg_dump against a provisioned PostgreSQL database.
package pgdump

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
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

	if err := stripPsqlMetaCommands(opts.OutputFile); err != nil {
		return fmt.Errorf("strip psql meta commands: %w", err)
	}

	return nil
}

// stripPsqlMetaCommands removes \restrict and \unrestrict lines from the dump
// file. pg_dump emits these psql meta-commands around COPY blocks when row
// level security policies exist; we don't need them in our output.
func stripPsqlMetaCommands(path string) error {
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
		if strings.HasPrefix(line, `\restrict`) || strings.HasPrefix(line, `\unrestrict`) {
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

func sanitizeKey(s string) string {
	r := strings.NewReplacer("/", "-", "\\", "-", " ", "-", ":", "-")
	return r.Replace(s)
}
