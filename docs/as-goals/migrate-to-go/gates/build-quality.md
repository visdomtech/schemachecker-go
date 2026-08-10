# Gate: Build & Quality

## Condition
The Go project builds cleanly, passes vet, and the CLI binary works with all 7 subcommands.

## Evidence Required
- [ ] `go build ./...` — produces binary without errors
- [ ] `go vet ./...` — passes with zero findings
- [ ] CLI binary responds to `schemachecker --help` with usage showing all 7 subcommands
- [ ] Each subcommand shows usage on invalid args (matching Java error behavior with exit code 99)
- [ ] `go.mod` and `go.sum` are committed and consistent

## Verification Method
- Run `go build ./...` and `go vet ./...` in the project root
- Execute binary with `--help` and with each subcommand with wrong arg count
- Verify go.mod has orcacommon dependency

## Owner
Senior Software Engineer
