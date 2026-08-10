# Gate: Pure File Commands

## Condition
The `split`, `merge`, `orphaned`, and `dirdiff` subcommands are implemented in Go with behavioral equivalence to the Java version.

## Evidence Required
- [ ] `cmd/split.go` — PGDumpSplitter state machine ported, splits pg_dump output into per-object SQL files with index.txt → `cmd/split.go`
- [ ] `cmd/merge.go` — MigrationFromIndexFile reads index and concatenates referenced SQL files → `cmd/merge.go`
- [ ] `cmd/orphaned.go` — OrphanedFilesFinder detects unreferenced and missing files → `cmd/orphaned.go`
- [ ] `cmd/dirdiff.go` — FileTreeDiffer compares directory trees with unified diff output → `cmd/dirdiff.go`
- [ ] CLI routing: all 4 subcommands accessible from main binary → `main.go`

## Verification Method
- Code review comparing Go implementation logic against Java source
- `go build ./...` succeeds
- `go vet ./...` passes
- CLI `--help` shows all subcommands

## Owner
Senior Software Engineer
