# schemachecker

A CLI tool for managing and validating PostgreSQL database schemas. It provides utilities to split pg_dump output into per-object SQL files, merge index-based schema definitions, detect orphaned files, compare directory trees, and validate that a schema definition matches incremental migrations.

Uses [orcacommon/postgres](https://github.com/visdomtech/orcacommon) for PostgreSQL provisioning via testcontainers and Atlas-based migrations.

## Build

```sh
go build -o schemachecker .
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
    - dirdiff
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

Splits a single pg_dump SQL file into per-object SQL files organized by schema and object type. Produces a directory tree and an `index.txt` that records the file creation order (useful for replay).

```
schemachecker split [schemaExportFile] [outputDir]
```

| Parameter | Description |
|-----------|-------------|
| `schemaExportFile` | Path to the pg_dump output SQL file to split. |
| `outputDir` | Path to the directory where split files are written. Created if it does not exist. |

The output directory structure mirrors PostgreSQL object types:

```
outputDir/
├── index.txt
├── SETTINGS.sql
├── EXTENSION/
│   └── plpgsql.sql
├── public/
│   ├── TABLE/
│   │   ├── users.sql
│   │   └── orders.sql
│   ├── SEQUENCE/
│   │   └── users_id_seq.sql
│   ├── FUNCTION/
│   │   └── my_func.sql
│   ├── CONSTRAINT/
│   │   └── users.sql
│   ├── FK_CONSTRAINT/
│   │   └── orders.sql
│   ├── INDEX/
│   ├── DEFAULT/
│   ├── TRIGGER/
│   └── DATA/
└── ...
```

**Exit codes:** `0` on success, `99` on invalid arguments.

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

Provisions a PostgreSQL database via testcontainers, runs Atlas migrations from a directory, and produces a pg_dump SQL file.

```
schemachecker dump [migrations] [outputFile]
```

| Parameter | Description |
|-----------|-------------|
| `migrations` | Path to the directory containing migration SQL files. |
| `outputFile` | Path where the pg_dump output is written. |

The pg_dump is run with `--no-privileges` and excludes `flyway_schema_history` and `atlas_schema_revisions` tables.

**Exit codes:** `0` on success, `99` on invalid arguments.

**Example:**

```sh
schemachecker dump ./migrations ./build/schema.sql
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
| `DUMP_DATA` | `check`, `validate`, `dump` | When **unset** (default), dumps are schema-only (`pg_dump --schema-only`). Set to any value to include data in dumps. |
| `DB_URL_TEMPLATE` | `check`, `validate`, `dump` | Database URL template for orcacommon. Defaults to `postgres:tc://...` which provisions a testcontainer. Override to connect to an existing PostgreSQL instance. |

## Exit Codes

| Code | Meaning |
|------|---------|
| `0` | Success |
| `1` | Comparison found differences (schemas differ, orphaned files found) |
| `3` | Infrastructure failure (pg_dump, DB provisioning, I/O error) |
| `99` | Invalid command-line arguments |

## Requirements

- Go 1.26+
- Docker (for testcontainer-based PostgreSQL provisioning used by `check`, `validate`, and `dump`)
- `pg_dump` available in `PATH` (used by `check`, `validate`, and `dump`)

## Differences from Java Version

This Go port is a faithful migration of the original Java/Gradle schemachecker tool with the following intentional differences:

- **CLI binary only** — the Gradle plugin (`SchemaCheckerPlugin`) is not ported. Use the CLI binary directly or wrap it in your build system.
- **No `initScript` parameter** — the Java version accepted an optional init script for database initialization. The Go version uses Atlas migrations exclusively via `orcacommon/postgres`.
- **Testcontainer provisioning** — PostgreSQL is provisioned via `orcacommon/postgres` testcontainers instead of direct JDBC connections. Control via `DB_URL_TEMPLATE` environment variable.
- **Exit code `3` for infrastructure failures** — the Java version used exit code `1` for all failures. The Go version distinguishes schema differences (exit `1`) from infrastructure failures (exit `3`).
- **Output directory cleanup** — the `check` command now cleans the output directory before running to prevent corruption from partial retries.
