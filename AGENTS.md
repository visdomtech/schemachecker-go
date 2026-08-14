# AGENTS.md

## Project Overview

**schemachecker-go** is a CLI tool for managing and validating PostgreSQL database schemas. It splits pg_dump output into per-object SQL files, merges index-based schema definitions, detects orphaned files, compares directory trees, and validates that a schema definition matches incremental migrations. Uses `orcacommon/postgres` for PostgreSQL provisioning via testcontainers and Atlas-based migrations.

## Tech Stack

- **Language:** Go 1.26+
- **Module:** `github.com/visdomtech/schemachecker-go`
- **Key dependencies:** `jackc/pgx/v5` (PostgreSQL driver), `testcontainers-go` (DB provisioning), `pmezard/go-difflib` (unified diff), `visdomtech/orcacommon` (shared PostgreSQL provisioning)
- **External tools:** `pg_dump` (must be in PATH), Docker (for testcontainer-based PostgreSQL)

## Build & Test Commands

```sh
go build -o schemachecker .                  # Build binary
go build ./...                               # Build all packages (verification)
go test ./...                                # Run all tests
go vet ./...                                 # Static analysis
go build -ldflags "-X main.version=$(cat version.txt)" -o schemachecker .  # Versioned build
```

There is no separate lint config or Makefile. Use `go build`, `go test`, `go vet` as the verification suite.

## Architecture

Single-module Go CLI. One `main.go` entry point dispatches to subcommand handlers in `cmd/`. Core logic lives in `internal/` packages.

```
main.go                  Entry point: arg parsing, subcommand switch, error handling with exit codes
cmd/                     Subcommand handlers (one file per command) + shared utilities
  root.go                Usage text constant, PrintUsage(), UsageError() helper
  check.go               RunCheck — merge schema index, dump both sides, split, dirdiff
  diff.go                RunDiff — wrap baseline SQL, dump both sides, split, dirdiff
  validate.go            RunValidate — dump two migration dirs, compare SQL files
  split.go               RunSplit — split pg_dump into per-object files
  merge.go               RunMerge — concatenate index-referenced SQL files
  orphaned.go            RunOrphaned — check index vs filesystem consistency
  dump.go                RunDump — provision DB, run migrations, pg_dump
  dirdiff.go             RunDirDiff — compare two split directories
internal/
  checkererror/          Typed error with exit codes (ExitDiff=1, ExitInfra=3, ExitUsage=99)
  dirdiff/               Directory tree comparison with unified diff output
  merge/                 Index file parsing and SQL concatenation
  orphaned/              Index-to-filesystem consistency checking
  pgdump/                PostgreSQL provisioning, migration execution, pg_dump invocation, post-processing
  split/                 pg_dump SQL splitting into per-object files by schema/type
```

## Coding Conventions

- **Error handling:** All errors returned as `*checkererror.Error` via `checkererror.New()` or `checkererror.Wrap()`. Exit codes: `ExitDiff` (1) for comparison differences, `ExitInfra` (3) for I/O or provisioning failures, `ExitUsage` (99) for invalid arguments.
- **Subcommand pattern:** Each handler is `func Run<Command>(args []string) error` in `cmd/`. Args[0] is the command name; positional args start at args[1]. Validate `len(args)` at entry, return `UsageError()` on mismatch.
- **Safety checks:** Commands that `os.RemoveAll` output directories must refuse dangerous paths (`/`, cwd, home directory).
- **Output:** Progress messages via `fmt.Printf` with bracket notation (e.g., `Exporting incremental migrations [path]`). Results to stdout. Errors to stderr via `main.go`.
- **File operations:** Use `filepath.Join` for path construction. Directory permissions `0o755`. Streaming `io.Copy` for file copies.
- **Migration naming:** Flyway-compatible convention (`V1.0.0__description.sql`) when wrapping SQL into migration folders.
- **pg_dump flags:** `--no-privileges`, `--no-owner`. Post-processing strips `\restrict`/`\unrestrict` psql meta-commands.
- **Testing:** Unit tests exist for `internal/merge` and `internal/split`. The `cmd/` package has no unit tests — testing is done via integration/CLI invocation.

## Environment Variables

- `DUMP_DATA` — when unset (default), dumps are schema-only. Set to any value to include data.
- `DB_URL_TEMPLATE` — database URL template for orcacommon. Defaults to testcontainer provisioning.

## Exit Codes

| Code | Meaning |
|------|---------|
| 0 | Success |
| 1 | Comparison found differences |
| 3 | Infrastructure failure |
| 99 | Invalid arguments |
