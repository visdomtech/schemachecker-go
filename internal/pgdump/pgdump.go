// Package pgdump executes pg_dump against a provisioned PostgreSQL database.
package pgdump

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"

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
	cmd.Env = append(os.Environ(), fmt.Sprintf("PGPASSWORD=%s", password))
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

	return nil
}

// ProvisionAndDump creates a testcontainer PostgreSQL, runs migrations from
// migrationDir, and produces a pg_dump to outputFile.
func ProvisionAndDump(ctx context.Context, migrationDir, outputFile string, schemaOnly bool) error {
	if strings.TrimSpace(migrationDir) == "" {
		return fmt.Errorf("migrationDir must not be empty")
	}
	key := fmt.Sprintf("schemachecker-%s", sanitizeKey(migrationDir))

	dbcfg := postgres.DBConfig{}
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
