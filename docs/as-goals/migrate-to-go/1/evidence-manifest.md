# Evidence Manifest — Iteration 1

## Gate Status

| Gate | Status | Evidence | Owner |
|------|--------|----------|-------|
| Pure File Commands | ✅ Pass | `internal/split/split.go`, `cmd/split.go`, `internal/merge/merge.go`, `cmd/merge.go`, `internal/orphaned/orphaned.go`, `cmd/orphaned.go`, `internal/dirdiff/dirdiff.go`, `cmd/dirdiff.go`, `main.go` | Engineer |
| DB Commands | ✅ Pass | `cmd/dump.go`, `cmd/validate.go`, `cmd/check.go`, `internal/pgdump/pgdump.go` | Engineer |
| Build & Quality | ✅ Pass | `go build ./...` PASS, `go vet ./...` PASS, CLI binary tested | Engineer |

## Return Shipments (Failed Gates)

None.

## Code Quality Findings
- Critical: 0
- Warning: 0 (1 fixed in `edee8eb`)
- Suggestion: 1 (PSQL meta-command normalization cosmetic difference)

## Commits Reviewed
- `8684298`: feat: migrate schemachecker from Java/Gradle to Go CLI
- `edee8eb`: fix: strip trailing newlines before splitting to match Java .lines() behavior
