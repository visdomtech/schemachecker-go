# Gate: DB Commands

## Condition
The `dump`, `validate`, and `check` subcommands are implemented in Go using `orcacommon/postgres` for PostgreSQL provisioning and Atlas for migrations.

## Evidence Required
- [ ] `cmd/dump.go` — Starts PostgreSQL via orcacommon, runs Atlas migrations, produces pg_dump SQL file → `cmd/dump.go`
- [ ] `cmd/validate.go` — Dumps two migration sets and diffs them → `cmd/validate.go`
- [ ] `cmd/check.go` — Orchestrates full schema-vs-migrations comparison (merge → dump → split → diff) → `cmd/check.go`
- [ ] `internal/pgdump/pgdump.go` — pg_dump execution against provisioned database → `internal/pgdump/pgdump.go`
- [ ] orcacommon integration: uses `postgres.Connect` and `postgres.Migrator` correctly → all DB command files

## Verification Method
- Code review verifying orcacommon/postgres API usage
- Verify pg_dump invocation matches Java behavior (--schema-only, --no-privileges, --exclude-table=flyway_schema_history)
- `go build ./...` succeeds
- `go vet ./...` passes

## Owner
Senior Software Engineer
