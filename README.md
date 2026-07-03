# code-index

`code-index` builds a local SQLite index for source-code navigation. It is intended for LLM and agent workflows that should query code structure through SQL instead of repeatedly using grep-style text search.

It is not intended to replace language servers, VCS indexes, or full code-intelligence systems; it provides a small, local, queryable index that agents can rebuild cheaply.

The binary is written in Go and uses the `sqlite3` command for database creation and queries. The built binary does not require a Go runtime.

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

Rebuild an index atomically:

```sh
./code-index rebuild /path/to/repo
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

## Git Hooks

You can rebuild the local index automatically after branch checkouts and merges. These hooks are optional and only run when `code-index` is available on `PATH`.

Refresh the index after switching branches:

```sh
cat > .git/hooks/post-checkout <<'EOF'
#!/bin/sh
# Args: previous HEAD, new HEAD, branch checkout flag.
[ "$3" = "1" ] || exit 0

root="$(git rev-parse --show-toplevel)" || exit 0
command -v code-index >/dev/null 2>&1 || exit 0

(
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
  code-index rebuild "$root" >/dev/null 2>&1
) &
EOF
chmod +x .git/hooks/post-merge
```

The examples run in the background so Git commands do not wait for indexing. Queries keep using the previous index until the rebuild finishes and replaces it.

## Schema

Main tables:

- `files`: repository-relative paths and file metadata
- `symbols`: regex-extracted definitions such as functions, methods, classes, modules, interfaces, traits, and types
- `lines`: indexed source lines
- `file_metrics`: per-file line, blank line, comment line, code line, and symbol counts
- `files_fts` and `symbols_fts`: FTS5 tables when the installed `sqlite3` supports FTS5

## Notes

This is a lightweight regex-based index, not a language server. Treat matches as navigation candidates and inspect source before making behavioral claims.

## License

MIT
