# schemachecker

A CLI tool for managing and validating PostgreSQL database schemas. It provides utilities to split pg_dump output into per-object SQL files, merge index-based schema definitions, detect orphaned files, compare directory trees, and validate that a schema definition matches incremental migrations.

Uses [orcacommon/postgres](https://github.com/visdomtech/orcacommon) for PostgreSQL provisioning via testcontainers and Atlas-based migrations.

## Build

```sh
go build -o schemachecker .
```

To embed a version string at build time:

```sh
go build -ldflags "-X main.version=$(cat version.txt)" -o schemachecker .
```

## Usage

```
schemachecker [command] [opts]

Where command:
    - check
    - validate
    - split
    - merge
    - orphaned
    - dump
    - diff
    - dirdiff
Options:
    --version, -v    Print version and exit
```

---

## Subcommands

### `check`

Validates that a schema definition (index file referencing SQL sources) produces the same database structure as a set of incremental migrations. Internally it merges the index into a single migration, dumps both schemas via PostgreSQL, splits them into per-object files, and diffs the results.

```
schemachecker check [schemaDefinitionIndex] [incrementalMigrations] [outputDirectory]
```

| Parameter | Description |
|-----------|-------------|
| `schemaDefinitionIndex` | Path to the index file that lists SQL source files defining the target schema. The index file contains one relative path per line; lines starting with `--` are comments. |
| `incrementalMigrations` | Path to the directory containing incremental migration SQL files (Atlas naming convention: `{version}_{description}.sql`). |
| `outputDirectory` | Path where intermediate and output files are written (merged migration, dumps, split directories). Created if it does not exist. |

**Exit codes:** `0` if schemas match, `1` if they differ, `99` on invalid arguments.

**Example:**

```sh
schemachecker check ./schema/index.sql ./migrations ./build/schemachecker
```

---

### `validate`

Dumps two sets of migrations into separate SQL files and compares them with a unified diff. Unlike `check`, this compares migration directories directly without going through the index-file merge step.

```
schemachecker validate [schemaDefinitionMigrations] [incrementalMigrations] [outputDirectory]
```

| Parameter | Description |
|-----------|-------------|
| `schemaDefinitionMigrations` | Path to the directory containing schema definition migration SQL files. |
| `incrementalMigrations` | Path to the directory containing incremental migration SQL files. |
| `outputDirectory` | Path where the two dump files (`schemaDefinitionMigrations.sql` and `incrementalMigrations.sql`) are written. Created if it does not exist. |

**Exit codes:** `0` if schemas are identical, `1` if they differ, `99` on invalid arguments.

**Example:**

```sh
schemachecker validate ./schema-migrations ./incremental-migrations ./build/output
```

---

### `split`

Splits a single pg_dump SQL file into per-object SQL files organized by schema and object type. Produces a directory tree and an `index.txt` that records the file creation order.

Table-owned child objects are always inlined into the file of the table that owns
them, so `INDEX`, `TRIGGER`, `DEFAULT` and `FK_CONSTRAINT` SQL ends up inside
`<schema>/TABLE/<table>.sql` and the per-type child files are removed. The merged
tree is the only output shape; there is no option in either direction.

```
schemachecker split [schemaExportFile] [outputDir]
```

| Parameter | Description |
|-----------|-------------|
| `schemaExportFile` | Path to the pg_dump output SQL file to split. |
| `outputDir` | Path to the directory where split files are written. It is cleaned before splitting, so dangerous paths (`/`, the current directory, the home directory) are refused. |

**`index.txt` is a creation order, not a replay order.** The index records the
order in which files were created, which is useful for enumerating the tree — but
because FK constraints now live inside the child table's own file, an
`ALTER TABLE … ADD CONSTRAINT … FOREIGN KEY …` statement can be emitted before the
referenced table's file appears in the index. Sequential replay with something like
`psql -f` over `index.txt` order is therefore no longer topologically safe. The
in-repo consumer `merge.FromIndex` is unaffected: it splits each file into
statement blocks and defers every FK block to a second pass, which is what `check`
and the `merge` subcommand use.

A second, known case: when a sequence is **not** folded into `bigserial`, merging
moves its `ALTER TABLE … ALTER COLUMN … SET DEFAULT nextval(…)` from `DEFAULT/` into
the owning `TABLE/<table>.sql`, which appears **earlier** in `index.txt` than the
`SEQUENCE/` file it refers to. Sequential replay then fails with
`ERROR: relation "<schema>_<table>_seq" does not exist` and the column silently
loses its default. This affects `IDENTITY`-adjacent and non-standard
(`CYCLE`/`MINVALUE`/`MAXVALUE`) sequences, is a pre-existing defect unrelated to
the bigserial matching fix, and is **not** fixed in this release — see
[`docs/caveat.md`](docs/caveat.md) §N1. Use `schemachecker merge`
rather than `psql -f` over `index.txt`.

The output directory structure mirrors PostgreSQL object types. The listing below
is from an actual run over a dump containing tables, sequences, indexes, triggers,
constraints, policies, a function, a view and table data — regenerate it for
yourself with `find <outputDir> | sort`:

```
outputDir/
├── index.txt
├── SETTINGS.sql
├── EXTENSION/
│   └── plpgsql.sql
├── SCHEMAS/
│   └── orca.sql
├── public/
│   ├── TABLE/
│   │   ├── users.sql
│   │   └── orders.sql
│   ├── POLICY/
│   │   └── users.sql
│   ├── FUNCTION/
│   │   └── audit.sql
│   ├── VIEW/
│   │   └── user_emails.sql
│   ├── DATA/
│   │   └── users.sql
│   └── SEQUENCE_SET/
│       └── users_id_seq.sql
└── orca/
    ├── TABLE/
    │   └── policy_drafts.sql
    └── SEQUENCE/
        └── policy_drafts_draft_id_seq.sql
```

What that tree tells you:

- `INDEX/`, `TRIGGER/`, `DEFAULT/` and `FK_CONSTRAINT/` normally disappear, because
  those child objects are appended into `TABLE/<table>.sql`. It is **not** a
  guarantee. The merge finds a child's owner either by filename (default,
  constraint, foreign key) or by the `ON <relation>` clause in the SQL (index,
  trigger), so an object stays in its own directory when neither can resolve to a
  table file. Observed survivors with real `pg_dump 17` output: indexes on
  materialized views, indexes on a partitioned parent (`ON ONLY …`), indexes whose
  relation needs double-quoting and contains a space, `INSTEAD OF` triggers on
  views, triggers on foreign tables, and `DEFAULT`/`FK_CONSTRAINT` files for such
  quoted relations — those files are additionally named after the first word of
  the relation (`DEFAULT/Weird.sql` for `public."Weird Name"`).
  `CONSTRAINT/` really is never emitted: primary-key and unique constraints are
  appended straight into the TABLE file while splitting, before any merge runs.
- `SEQUENCE/` survives for `IDENTITY` columns, for sequences with non-standard
  options (`CYCLE`, an explicit `MINVALUE` or `MAXVALUE`), for sequences owned by a
  column that is not a plain `integer`/`bigint NOT NULL` (including `smallint` —
  `smallserial` is never generated), for sequences whose `OWNED BY` relation needs
  double-quoting, and whenever the `nextval` default cannot be removed from every
  file that holds it. A plain `OWNED BY` sequence on an `integer`/`bigint
  NOT NULL` column is folded into the column as `bigserial`, its `DEFAULT`
  assignment is deleted and its sequence file is removed. Only the column named in
  `OWNED BY` is rewritten — sibling columns such as `user_id` or `external_id` keep
  their declared type, and a quoted mixed-case column (`"UserId"`) is matched
  exactly, not case-insensitively.
- `POLICY/`, `FUNCTION/`, `VIEW/`, `MATERIALIZED VIEW/`, `FOREIGN TABLE/`, `COMMENT/`,
  `RULE/`, `ROW SECURITY/`, `TABLE ATTACH/`, `INDEX ATTACH/`, `DATA/`, `EXTENSION/`
  and `SCHEMAS/` keep their own files; `POLICY/` files are named after the table
  they protect (`users.sql`), not after the policy.
- `DATA/` and `SEQUENCE_SET/` only appear when the dump carries rows or sequence
  values, i.e. when the dump was taken with data rather than `--schema-only`.

**Exit codes:** `0` on success, `3` if the dump could not be read or the merged tree
could not be written (the merge pass is on `split`'s critical path), `99` on invalid arguments.

**Example:**

```sh
pg_dump --schema-only mydb > dump.sql
schemachecker split dump.sql ./split-output
```

---

### `merge`

Reads an index file and concatenates all referenced SQL files into a single migration file. Lines in the index file starting with `--` are treated as comments and preserved. Blank lines are preserved. All other lines are interpreted as relative paths to SQL files.

```
schemachecker merge [indexfile] [migrationfile]
```

| Parameter | Description |
|-----------|-------------|
| `indexfile` | Path to the index file listing SQL files to merge (one per line, relative to the index file's directory). |
| `migrationfile` | Path to the output migration file. |

The output file ends with `SET check_function_bodies = true;` to reset PostgreSQL's function body checking after all definitions are loaded.

**Exit codes:** `0` on success, `99` on invalid arguments.

**Example:**

```sh
schemachecker merge ./schema/index.sql ./build/V1.0.0__migrationFile.sql
```

---

### `orphaned`

Checks for consistency between an index file and the filesystem. Reports files that exist on disk but are not referenced from the index (orphans), and files referenced from the index but missing on disk.

```
schemachecker orphaned [indexfile]
```

| Parameter | Description |
|-----------|-------------|
| `indexfile` | Path to the index file to validate against the filesystem. All non-comment, non-blank lines are treated as relative file paths. |

**Exit codes:** `0` if no orphans or missing files, `1` if inconsistencies found, `99` on invalid arguments.

**Example:**

```sh
schemachecker orphaned ./schema/index.sql
```

---

### `dump`

Provisions a PostgreSQL database via testcontainers, runs Atlas migrations from a directory, and produces a pg_dump SQL file — or a split per-object directory with `--split`.

```
schemachecker dump [migrations] [outputPath] [--split]
```

| Parameter | Description |
|-----------|-------------|
| `migrations` | Path to the directory containing migration SQL files. |
| `outputPath` | Path where the pg_dump output is written. Without `--split` this is a single SQL file whose parent directory must already exist; with `--split` it is a directory that is cleaned first, so dangerous paths are refused. |
| `--split` | Optional. Splits the dump into per-object files under `outputPath`. |

With `--split`, the directory produced has exactly the merged shape described in the
[`split` section](#split): table-owned `INDEX`, `TRIGGER`, `DEFAULT` and
`FK_CONSTRAINT` SQL is inlined into `<schema>/TABLE/<table>.sql`, `POLICY`, `VIEW`,
`FUNCTION`, `SEQUENCE` (for `IDENTITY` and non-standard sequences), `EXTENSION` and
`SCHEMAS` keep their own files, and `index.txt` is pruned to match. `DUMP_DATA`
controls whether the dump carries rows, and therefore whether `DATA/` and
`SEQUENCE_SET/` appear.

The pg_dump is run with `--no-privileges` and `--no-owner`, and excludes `flyway_schema_history` and `atlas_schema_revisions` tables.

**Exit codes:** `0` on success, `3` on provisioning, pg_dump or split/merge I/O failure, `99` on invalid arguments.

**Example:**

```sh
schemachecker dump ./migrations ./build/schema.sql

# same dump, as a merged per-object tree instead of one file
schemachecker dump ./migrations ./build/split-output --split
```

---

### `diff`

Wraps a baseline SQL file into a migration folder, dumps both the baseline and an incremental migrations directory via PostgreSQL, splits them into per-object files, and compares the results with unified diff output.

```
schemachecker diff [migrationsDir] [baselineFile] [outputDir]
```

| Parameter | Description |
|-----------|-------------|
| `migrationsDir` | Path to the directory containing incremental migration SQL files. |
| `baselineFile` | Path to the baseline SQL file to compare against. |
| `outputDir` | Path where intermediate and output files are written. Created if it does not exist. |

**Exit codes:** `0` if schemas match, `1` if they differ, `99` on invalid arguments.

**Example:**

```sh
schemachecker diff ./migrations ./baseline.sql ./build/diff-output
```

---

### `dirdiff`

Compares two directory trees file-by-file and reports added, removed, and changed files. Changed files are shown with unified diff output. Recognizes psql meta-commands (`\restrict`, `\unrestrict`) and treats them as equivalent regardless of arguments.

```
schemachecker dirdiff [left] [right]
```

| Parameter | Description |
|-----------|-------------|
| `left` | Path to the left directory. |
| `right` | Path to the right directory. |

Output sections:
- **Added** — files present in `right` but not in `left`
- **Removed** — files present in `left` but not in `right`
- **Changed** — unified diff of files that differ

**Exit codes:** `0` if directories are identical, `1` if they differ, `99` on invalid arguments.

**Example:**

```sh
schemachecker dirdiff ./split-schema ./split-migrations
```

---

## Environment Variables

| Variable | Used By | Description |
|----------|---------|-------------|
| `DUMP_DATA` | `check`, `validate`, `diff`, `dump` | When **unset** (default), dumps are schema-only (`pg_dump --schema-only`). Set to any value to include data in dumps. |
| `DB_URL_TEMPLATE` | `check`, `validate`, `diff`, `dump` | Database URL template for orcacommon. Defaults to `postgres:tc://...` which provisions a testcontainer. Override to connect to an existing PostgreSQL instance. |

## Exit Codes

| Code | Meaning |
|------|---------|
| `0` | Success |
| `1` | Comparison found differences (schemas differ, orphaned files found) |
| `3` | Infrastructure failure (pg_dump, DB provisioning, I/O error) |
| `99` | Invalid command-line arguments |

## Requirements

- Go 1.26+
- Docker (for testcontainer-based PostgreSQL provisioning used by `check`, `validate`, `diff`, and `dump`)
- `pg_dump` available in `PATH` (used by `check`, `validate`, `diff`, and `dump`)

## Differences from Java Version

This Go port is a faithful migration of the original Java/Gradle schemachecker tool with the following intentional differences:

- **CLI binary only** — the Gradle plugin (`SchemaCheckerPlugin`) is not ported. Use the CLI binary directly or wrap it in your build system.
- **No `initScript` parameter** — the Java version accepted an optional init script for database initialization. The Go version uses Atlas migrations exclusively via `orcacommon/postgres`.
- **Testcontainer provisioning** — PostgreSQL is provisioned via `orcacommon/postgres` testcontainers instead of direct JDBC connections. Control via `DB_URL_TEMPLATE` environment variable.
- **Exit code `3` for infrastructure failures** — the Java version used exit code `1` for all failures. The Go version distinguishes schema differences (exit `1`) from infrastructure failures (exit `3`).
- **Output directory cleanup** — the `check` command now cleans the output directory before running to prevent corruption from partial retries.
