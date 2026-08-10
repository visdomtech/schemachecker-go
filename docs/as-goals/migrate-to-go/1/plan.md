# Plan — Iteration 1

## Project Structure
```
schemachecker-go/
├── main.go                    — CLI entry with subcommand routing
├── cmd/
│   ├── root.go               — shared CLI helpers, usage text, error handling
│   ├── split.go              — split subcommand
│   ├── merge.go              — merge subcommand
│   ├── orphaned.go           — orphaned subcommand
│   ├── dirdiff.go            — dirdiff subcommand
│   ├── dump.go               — dump subcommand
│   ├── validate.go           — validate subcommand
│   └── check.go              — check subcommand
├── internal/
│   ├── checkererror/
│   │   └── error.go          — CheckerError type (exit code + message)
│   ├── split/
│   │   └── split.go          — PGDumpSplitter state machine
│   ├── merge/
│   │   └── merge.go          — MigrationFromIndexFile
│   ├── orphaned/
│   │   └── orphaned.go       — OrphanedFilesFinder
│   ├── dirdiff/
│   │   └── dirdiff.go        — FileTreeDiffer with unified diff
│   └── pgdump/
│       └── pgdump.go         — pg_dump execution via os/exec
└── go.mod / go.sum
```

## Ordered Tasks

### Task 1: Project scaffold + error type
- **Files:** `go.mod`, `main.go`, `internal/checkererror/error.go`, `cmd/root.go`
- **Acceptance:** `go build ./...` succeeds, binary prints usage on no args and exits 1

### Task 2: split command
- **Files:** `internal/split/split.go`, `cmd/split.go`
- **Acceptance:** Port the full PGDumpSplitter state machine from Java. Regex patterns, state transitions, buffer management, index.txt generation all match.

### Task 3: merge command
- **Files:** `internal/merge/merge.go`, `cmd/merge.go`
- **Acceptance:** Reads index file, concatenates referenced SQL files into output, handles comments and blank lines, validates file readability.

### Task 4: orphaned command
- **Files:** `internal/orphaned/orphaned.go`, `cmd/orphaned.go`
- **Acceptance:** Walks directory tree, compares against index references, reports orphans and missing files.

### Task 5: dirdiff command
- **Files:** `internal/dirdiff/dirdiff.go`, `cmd/dirdiff.go`
- **Acceptance:** Compares two directory trees, reports added/removed/changed files with unified diff. Handles ignore patterns and PSQL meta commands.

### Task 6: pgdump helper
- **Files:** `internal/pgdump/pgdump.go`
- **Acceptance:** Executes `pg_dump` via `os/exec` with connection parameters, supports --schema-only, --no-privileges, --exclude-table flags.

### Task 7: dump command
- **Files:** `cmd/dump.go`
- **Acceptance:** Uses orcacommon/postgres.Connect for testcontainer provisioning, runs migrations via Migrator, calls pg_dump to produce output SQL file.

### Task 8: validate command
- **Files:** `cmd/validate.go`
- **Acceptance:** Dumps two migration sets to separate files and diffs them using the dirdiff/diff logic.

### Task 9: check command
- **Files:** `cmd/check.go`
- **Acceptance:** Orchestrates merge → dump → split → dump → split → dirdiff pipeline matching Java Checker.run flow.

### Task 10: Wire all subcommands + final build
- **Files:** `main.go`
- **Acceptance:** All 7 subcommands routed, `go build` and `go vet` pass clean.
