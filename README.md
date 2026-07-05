# code-index

`code-index` builds a local SQLite index for source-code navigation. It is intended for LLM and agent workflows that should query code structure through SQL instead of repeatedly using grep-style text search.

It is not intended to replace language servers, VCS indexes, or full code-intelligence systems; it provides a small, local, queryable index that agents can rebuild cheaply.

The binary is written in Go and uses the `sqlite3` command for database creation and queries. The built binary does not require a Go runtime.

The reusable agent skill source lives at `skills/code-index/SKILL.md`. It is written to be usable by coding agents such as Codex or Claude, and prefers an external `code-index` binary on `PATH`.

For local agent-skill development, keep the checked-in skill as the source of truth and symlink or copy it into the skill directory used by your agent runtime. For Codex:

```sh
mkdir -p "${CODEX_HOME:-$HOME/.codex}/skills"
ln -s "$PWD/skills/code-index" "${CODEX_HOME:-$HOME/.codex}/skills/code-index"
```

If the target already exists, remove or rename it first.

Host-specific metadata can live under `skills/code-index/agents/`; agents that do not use those files can ignore them.

## Build

```sh
go build -o code-index .
```

## Usage

Initialize an empty index database:

```sh
./code-index init /path/to/repo
```

`init` creates the schema and metadata only. It fails if the index database already exists.

Rebuild an index atomically from Git-tracked files:

```sh
./code-index rebuild /path/to/repo
```

`rebuild` requires a Git work tree and indexes files reported by `git ls-files`. If another `init`, `rebuild`, or `update` is already running for the same database, `rebuild` skips and exits successfully.

Update an existing index incrementally:

```sh
./code-index update /path/to/repo
```

`update` requires a Git work tree. It refreshes changed Git-tracked files and removes files that are no longer tracked. It requires an existing database, so run `init` or `rebuild` first.

Show index status:

```sh
./code-index status --root /path/to/repo
```

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

You can refresh the local index automatically after branch checkouts and merges. These hooks are optional and only run when `code-index` is available on `PATH`.

Refresh the index after switching branches:

```sh
cat > .git/hooks/post-checkout <<'EOF'
#!/bin/sh
# Args: previous HEAD, new HEAD, branch checkout flag.
[ "$3" = "1" ] || exit 0

root="$(git rev-parse --show-toplevel)" || exit 0
command -v code-index >/dev/null 2>&1 || exit 0

(
  code-index update "$root" >/dev/null 2>&1 ||
    code-index rebuild "$root" >/dev/null 2>&1
) &
EOF
chmod +x .git/hooks/post-checkout
```

Refresh the index after pulls or merges:

```sh
cat > .git/hooks/post-merge <<'EOF'
#!/bin/sh
root="$(git rev-parse --show-toplevel)" || exit 0
command -v code-index >/dev/null 2>&1 || exit 0

(
  code-index update "$root" >/dev/null 2>&1 ||
    code-index rebuild "$root" >/dev/null 2>&1
) &
EOF
chmod +x .git/hooks/post-merge
```

The examples run in the background so Git commands do not wait for indexing. During `init`, `rebuild`, and `update`, `code-index` writes a `.lock` file next to the target database. Queries keep using a consistent SQLite snapshot and print a warning to stderr while the lock is present. If no previous index exists yet, queries fail with a message that initialization or rebuilding is still in progress.

`status` combines the lock file with database metadata. Lock fields describe a currently running operation; metadata such as `updated_at`, `last_operation`, `vcs_revision`, and `vcs_ref` describes the last successful update.

## Schema

Main tables:

- `meta`: schema version, file source, hash algorithm, last successful update time, operation, and VCS revision metadata
- `files`: repository-relative paths and file metadata
- `symbols`: regex-extracted definitions such as functions, methods, classes, modules, interfaces, traits, and types
- `lines`: indexed source lines
- `file_metrics`: per-file line, blank line, comment line, code line, and symbol counts
- `files_fts` and `symbols_fts`: FTS5 tables when the installed `sqlite3` supports FTS5

## Notes

This is a lightweight regex-based index, not a language server. Treat matches as navigation candidates and inspect source before making behavioral claims.

## License

MIT
