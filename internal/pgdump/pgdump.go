// Package pgdump executes pg_dump against a provisioned PostgreSQL database.
package pgdump

import (
	"context"
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

	slog.Info("running pg_dump", "host", host, "port", port, "db", dbname, "args", args)

	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Env = append(os.Environ(), fmt.Sprintf("PGPASSWORD=%s", password))

	output, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return fmt.Errorf("pg_dump failed: %s\n%s", err, string(exitErr.Stderr))
		}
		return fmt.Errorf("pg_dump failed: %w", err)
	}

	if err := os.WriteFile(opts.OutputFile, output, 0o644); err != nil {
		return fmt.Errorf("write dump output: %w", err)
	}

	return nil
}

// ProvisionAndDump creates a testcontainer PostgreSQL, runs migrations from
// migrationDir, and produces a pg_dump to outputFile.
func ProvisionAndDump(ctx context.Context, migrationDir, outputFile, initScript string, schemaOnly bool) error {
	key := fmt.Sprintf("schemachecker-%s", sanitizeKey(migrationDir))

	dbcfg := postgres.DBConfig{}
	if initScript != "" {
		// orcacommon's testcontainer doesn't natively support init scripts via DBConfig,
		// but the migration dir should handle initialization.
		slog.Warn("initScript specified but orcacommon testcontainer uses Atlas migrations; initScript is ignored", "initScript", initScript)
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
