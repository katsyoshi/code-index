# code-index

`code-index` builds a local SQLite index for source-code navigation. It is intended for LLM and agent workflows that should query code structure through SQL instead of repeatedly using grep-style text search.

It is not intended to replace language servers, VCS indexes, or full code-intelligence systems; it provides a small, local, queryable index that agents can rebuild cheaply.

The binary is written in Go and uses the `sqlite3` command for database creation and queries. The built binary does not require a Go runtime.

The reusable agent skill source lives at `skills/code-index/SKILL.md`. It is written to be usable by coding agents such as Codex or Claude. Agents should use an explicit `CODE_INDEX_BIN` path instead of searching `PATH`, so they do not accidentally run a different `code-index` binary.

## Design

`code-index` is a lightweight retrieval layer for agents, not a language server. It is designed to reduce how much source an LLM needs to read, not to expand context.

- Query the local index first, then read only the source needed for the task.
- Keep the index rebuildable and local.
- Treat Markdown as documentation or notes, not as the index database.

## Install

`code-index` requires `git`, the `sqlite3` command, and Go for installation.

Choose the installed skill directory for your agent runtime. This is the directory that contains `SKILL.md`:

```sh
SKILL_DIR=/path/to/installed/skills/code-index
```

Install the binary under that skill directory:

```sh
mkdir -p "$SKILL_DIR/exec"
GOBIN="$SKILL_DIR/exec" go install github.com/katsyoshi/code-index@latest
```

Point agents and hooks at the exact binary. `CODE_INDEX_BIN` must be set in the environment used by the agent runtime; `code-index` may be on `PATH` for humans, but the skill does not rely on `PATH`.

```sh
export CODE_INDEX_BIN="$SKILL_DIR/exec/code-index"
"$CODE_INDEX_BIN" version
```

`version` prints the build commit hash when available, plus schema metadata useful for compatibility checks.

For agents and scripts, use JSON output:

```sh
"$CODE_INDEX_BIN" version --format json
```

The JSON format emits one object. `modified` is a boolean and `schema_version` is a number; unavailable build information is `null`.

For local development in this repository, build the checked-out source and point `CODE_INDEX_BIN` at that binary:

```sh
SKILL_DIR=/path/to/installed/skills/code-index
mkdir -p "$SKILL_DIR/exec"
go build -o "$SKILL_DIR/exec/code-index" .
export CODE_INDEX_BIN="$SKILL_DIR/exec/code-index"
```

For Codex local skill development, you can symlink the checked-in skill into Codex's skill directory first:

```sh
SKILL_DIR="${CODEX_HOME:-$HOME/.codex}/skills/code-index"
mkdir -p "${CODEX_HOME:-$HOME/.codex}/skills"
ln -s "$PWD/skills/code-index" "$SKILL_DIR"
```

If the target already exists, remove or rename it first. For other agent runtimes, set `SKILL_DIR` to that runtime's installed `skills/code-index` directory and configure `CODE_INDEX_BIN` using that runtime's normal environment mechanism.

Host-specific metadata can live under `skills/code-index/agents/`; agents that do not use those files can ignore them.

## Usage

Update an existing index incrementally:

```sh
./code-index update /path/to/repo
./code-index update --format json /path/to/repo
```

`update` requires an existing index database and a Git work tree. It refreshes changed Git-tracked files and removes files that are no longer tracked. If the index does not exist yet, run `init` or `rebuild` first.

`update` refuses incompatible indexes instead of silently mixing metadata. It asks for `rebuild` when schema, file source, hash, or indexing config settings do not match. If the database was created from another checkout path or Git history and you intentionally want this checkout to take it over, run:

```sh
./code-index update --adopt /path/to/repo
```

Its change counts are file counts: `added_files`, `updated_files`, and `deleted_files`. `symbols` reports symbols indexed from added or updated files during that update; use `stats` or `metrics` for index-wide totals.

Rebuild an index atomically from Git-tracked files:

```sh
./code-index rebuild /path/to/repo
./code-index rebuild --format json /path/to/repo
```

`rebuild` requires a Git work tree and indexes files reported by `git ls-files`. If another `init`, `rebuild`, or `update` is already running for the same database, `rebuild` skips and exits successfully.

Initialize an empty index database:

```sh
./code-index init /path/to/repo
./code-index init --format json /path/to/repo
```

`init` creates the schema and metadata only. It fails if the index database already exists.

For `init`, `rebuild`, and `update`, the JSON format emits one operation-result object with native counts and booleans. If `rebuild` or `update` skips because another index operation holds the lock, it exits successfully with `skipped: true`, `reason: "locked"`, and unavailable result fields set to `null`; the warning remains on stderr.

Show index status:

```sh
./code-index status --root /path/to/repo
./code-index status --root /path/to/repo --format json
```

`status` reports database metadata, current lock state, current Git head/branch/dirty state, and `index_stale`. A dirty work tree is fresh when it matches the dirty snapshot recorded by the last successful index operation.

The JSON format is intended for agents and scripts. It emits one object, uses native JSON booleans and numbers, and represents unavailable values as `null`.

It also reports whether `update` can safely proceed, whether `update --adopt` would be required, or whether `rebuild` is required.

Show command help:

```sh
./code-index help
./code-index help update
./code-index help --format json
./code-index help --format json update
```

The JSON format returns the top-level usage and command metadata. Whole-program help contains a `commands` array; command-specific help contains one object with `name`, `usage`, and `summary`.

Find symbol definitions:

```sh
./code-index defs --root /path/to/repo --list --format json
./code-index defs --root /path/to/repo parse_config
./code-index defs --root /path/to/repo --format json parse_config
```

Use `--list` without `QUERY` to list definitions ordered by path and source position. `--kind`, `--language`, and `--limit` apply to both listing and searching. Combining `--list` with `QUERY` is an error.

Find files:

```sh
./code-index files --root /path/to/repo --list --format json
./code-index files --root /path/to/repo config
./code-index files --root /path/to/repo --format json config
```

Use `--list` without `QUERY` to list files that are actually present in the index, ordered by path. `--language` and `--limit` apply to both listing and searching. Combining `--list` with `QUERY` is an error.

Run read-only SQL:

```sh
./code-index sql --root /path/to/repo \
  "select path, line, kind, name, signature from symbols where name like '%parse%' order by path, line limit 50"

./code-index sql --root /path/to/repo --format json \
  "select path, line, kind, name, signature from symbols where name like '%parse%' order by path, line limit 50"
```

The JSON format emits an array of objects with SQLite numbers and nulls preserved. Use unique column names or explicit aliases in JSON queries; duplicate column names are ambiguous when represented as object fields.

Show indexed source around a line:

```sh
./code-index show --root /path/to/repo --line 42 lib/config.rb
./code-index show --root /path/to/repo --line 42 --format json lib/config.rb
```

Show the current index tables and columns:

```sh
./code-index schema --root /path/to/repo
./code-index schema --root /path/to/repo --format json
```

`schema` reports user-facing tables, virtual tables, column types, nullability, and primary-key positions. SQLite and FTS5 internal tables are omitted.
The JSON format emits an array with native numbers and booleans; `key` is `null` for columns that are not part of a primary key.

Show indexed code metrics:

```sh
./code-index metrics --root /path/to/repo
./code-index metrics --root /path/to/repo lib/config
./code-index metrics --root /path/to/repo --format json
```

For `defs`, `files`, `show`, and `metrics`, the JSON format emits an array of objects, uses native JSON numbers, preserves nullable fields as `null`, and emits `[]` when there are no rows.

Show index-wide counts and build metadata:

```sh
./code-index stats --root /path/to/repo
./code-index stats --root /path/to/repo --format json
```

The JSON format emits one object with native counts and booleans. Unavailable metadata fields are `null`.

The default database is stored under `CODE_INDEX_CACHE_DIR` when set. Otherwise it uses `$XDG_CACHE_HOME/code-index` or `~/.cache/code-index`, keyed by the absolute repository path. Use `--db` to provide an explicit database path.

Print the default database path without creating it:

```sh
./code-index path /path/to/repo
./code-index path --format json /path/to/repo
```

The JSON format emits one object with a `path` field.

`rebuild` and `update` index Git-tracked files only. Initialize Git and add files before indexing a directory.

## Git Hooks

You can refresh the local index automatically after branch checkouts and merges. These hooks are optional and only run when `CODE_INDEX_BIN` points to an executable `code-index` binary. If Git runs hooks outside your shell environment, set `CODE_INDEX_BIN` inside the hook or from the environment that launches Git.

Refresh the index after switching branches:

```sh
cat > .git/hooks/post-checkout <<'EOF'
#!/bin/sh
# Args: previous HEAD, new HEAD, branch checkout flag.
[ "$3" = "1" ] || exit 0

root="$(git rev-parse --show-toplevel)" || exit 0
[ -n "${CODE_INDEX_BIN:-}" ] || exit 0
[ -x "$CODE_INDEX_BIN" ] || exit 0

(
  "$CODE_INDEX_BIN" update "$root" >/dev/null 2>&1 ||
    "$CODE_INDEX_BIN" rebuild "$root" >/dev/null 2>&1
) &
EOF
chmod +x .git/hooks/post-checkout
```

Refresh the index after pulls or merges:

```sh
cat > .git/hooks/post-merge <<'EOF'
#!/bin/sh
root="$(git rev-parse --show-toplevel)" || exit 0
[ -n "${CODE_INDEX_BIN:-}" ] || exit 0
[ -x "$CODE_INDEX_BIN" ] || exit 0

(
  "$CODE_INDEX_BIN" update "$root" >/dev/null 2>&1 ||
    "$CODE_INDEX_BIN" rebuild "$root" >/dev/null 2>&1
) &
EOF
chmod +x .git/hooks/post-merge
```

The examples run in the background so Git commands do not wait for indexing. During `init`, `rebuild`, and `update`, `code-index` writes a `.lock` file next to the target database. Queries keep using a consistent SQLite snapshot and print a warning to stderr while the lock is present. If no previous index exists yet, queries fail with a message that indexing is still in progress.

If a lock file records a PID that is no longer running, build/update/query commands treat it as stale and remove it before continuing. `status` reports `lock_stale` for visibility without requiring a rebuild.

`status` combines the lock file with database metadata. Lock fields describe a currently running operation; metadata such as `indexed_at`, `last_operation`, `vcs_head`, and `vcs_branch` describes the last successful update.

## Schema

Main tables:

- `meta`: schema version, file source, hash algorithm, last successful index time, operation, and VCS metadata such as head, branch, tracked-file dirty state, and dirty snapshot hash
- `files`: repository-relative paths and file metadata
- `symbols`: regex-extracted definitions such as functions, methods, classes, modules, interfaces, traits, and types
- `lines`: indexed source lines
- `file_metrics`: per-file line, blank line, comment line, code line, and symbol counts
- `files_fts` and `symbols_fts`: FTS5 tables when the installed `sqlite3` supports FTS5

## Notes

This is a lightweight regex-based index, not a language server. Treat matches as navigation candidates and inspect source before making behavioral claims.

## License

MIT
