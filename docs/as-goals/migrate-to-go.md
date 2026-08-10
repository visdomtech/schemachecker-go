# Migrate SchemaChecker to Go

## Goal
All 7 CLI subcommands of the Java/Gradle schemachecker tool are reimplemented in Go as a standalone CLI binary at `/Users/jiangzhaohua/visdom/schemachecker-go`, using `orcacommon/postgres` for PostgreSQL provisioning and Atlas migrations.

## Context
- **Source:** Java/Gradle tool at `/Users/jiangzhaohua/codes/visdomtech/schemachecker` with 7 subcommands: `check`, `validate`, `split`, `merge`, `orphaned`, `dump`, `dirdiff`
- **Target:** Empty Go module at `/Users/jiangzhaohua/visdom/schemachecker-go` (`github.com/visdomtech/schemachecker-go`)
- **Dependency:** `github.com/visdomtech/orcacommon` at `/Users/jiangzhaohua/visdom/orcacommon` — provides PostgreSQL testcontainer provisioning (`postgres.Connect`), Atlas-based migrations (`postgres.Migrator`), and connection pooling (`postgres.OpenPool`)
- **Key decisions:** CLI binary only (no Gradle plugin), use orcacommon for DB + migrations, use Atlas for migration execution

## Success Criteria
- All 7 subcommands (`check`, `validate`, `split`, `merge`, `orphaned`, `dump`, `dirdiff`) work with equivalent behavior to the Java version
- `split` correctly parses pg_dump output and splits into per-object SQL files with index.txt
- `merge` reads an index file and concatenates referenced SQL files into a single migration
- `orphaned` detects files not referenced from index and vice versa
- `dirdiff` compares two directory trees and reports added/removed/changed files with unified diff
- `dump` starts PostgreSQL via testcontainer, runs migrations via Atlas, and produces a pg_dump SQL file
- `validate` dumps two migration sets and diffs them
- `check` orchestrates the full schema-vs-migrations comparison pipeline
- `go build` produces a working binary
- `go vet ./...` passes clean

## Constraints
- Must use `github.com/visdomtech/orcacommon/postgres` for PostgreSQL provisioning and migrations
- Must support testcontainer-based PostgreSQL (the `postgres:tc://` URL scheme)
- pg_dump must be executable against the provisioned database (via `os/exec` with connection parameters)

## Out of Scope
- Gradle plugin (Go version is CLI-only)
- Docker container image building (jib equivalent)
- Maven/Gradle publishing
- Cloud Build configuration

## Created
2026-08-10
