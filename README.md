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

Build or refresh the index:

```sh
./code-index update /path/to/repo
```

`update` requires a Git work tree. It creates the database on first use, refreshes changed Git-tracked files, and removes files that are no longer tracked.

Its change counts are file counts: `added_files`, `updated_files`, and `deleted_files`. `symbols` reports symbols indexed from added or updated files during that update; use `stats` or `metrics` for index-wide totals.

Rebuild an index atomically from Git-tracked files:

```sh
./code-index rebuild /path/to/repo
```

`rebuild` requires a Git work tree and indexes files reported by `git ls-files`. If another `init`, `rebuild`, or `update` is already running for the same database, `rebuild` skips and exits successfully.

Initialize an empty index database when explicitly needed:

```sh
./code-index init /path/to/repo
```

`init` creates the schema and metadata only. It fails if the index database already exists.

Show index status:

```sh
./code-index status --root /path/to/repo
```

`status` reports database metadata, current lock state, current Git head/branch/dirty state, and `index_stale`. A dirty work tree is fresh when it matches the dirty snapshot recorded by the last successful index operation.

Find symbol definitions:

```sh
./code-index defs --root /path/to/repo parse_config
```

Find files:

```sh
./code-index files --root /path/to/repo config
```

Run read-only SQL:

```sh
./code-index sql --root /path/to/repo \
  "select path, line, kind, name, signature from symbols where name like '%parse%' order by path, line limit 50"
```

Show indexed source around a line:

```sh
./code-index show --root /path/to/repo --line 42 lib/config.rb
```

Show indexed code metrics:

```sh
./code-index metrics --root /path/to/repo
./code-index metrics --root /path/to/repo lib/config
```

The default database is stored under `CODE_INDEX_CACHE_DIR` when set. Otherwise it uses `$XDG_CACHE_HOME/code-index` or `~/.cache/code-index`, keyed by the absolute repository path. Use `--db` to provide an explicit database path.

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
