---
name: code-index
description: Build and use a local SQLite index for code navigation instead of ad hoc grep/rg/find searches. Use when an AI coding agent needs to locate files, classes, functions, methods, symbols, definitions, references, source lines, code metrics, or index status across an unfamiliar repository; when repeated codebase lookup would otherwise use shell text search; or when the user asks for SQL-backed, database-backed, or indexed code search.
---

# Code Index

## Overview

Use `code-index` as the first search surface for codebase navigation. Prefer SQL-backed queries for locating files, definitions, methods, classes, relevant source lines, metrics, and index status.

Use an external `code-index` binary on `PATH`. If an agent runtime installs this skill with a bundled `scripts/code-index` helper, treat that helper only as a fallback because it may lag the standalone CLI.

## Workflow

1. Resolve the target repository root.
2. Select the tool:

```bash
if ! command -v code-index >/dev/null 2>&1; then
  echo "code-index is not available on PATH" >&2
  exit 1
fi

TOOL="$(command -v code-index)"
```

3. In sandboxed agent sessions, prefer a writable cache directory unless the user gave a DB path:

```bash
export CODE_INDEX_CACHE_DIR="${CODE_INDEX_CACHE_DIR:-/tmp/code-index}"
```

4. Build or refresh the index before substantial search work. Prefer `rebuild`; if the selected fallback tool is old and does not support it, use `build`:

```bash
"$TOOL" rebuild "$PWD" || "$TOOL" build "$PWD"
```

5. Check status when lock or freshness may matter:

```bash
"$TOOL" status --root "$PWD"
```

If `status` is unsupported, continue with query commands and rely on rebuild output.

6. Search definitions and files through the index:

```bash
"$TOOL" defs --root "$PWD" parse_config
"$TOOL" files --root "$PWD" config
```

7. Run raw read-only SQL for precise lookup:

```bash
"$TOOL" sql --root "$PWD" \
  "select path, line, kind, name, signature from symbols where name like '%parse%' order by path, line limit 50"
```

8. Show source around indexed lines:

```bash
"$TOOL" show --root "$PWD" --line 42 lib/config.rb
```

9. Rebuild the index after editing files that affect search results.

## Search Policy

- Prefer this skill's SQLite index before using `grep`, `rg`, `find`, or broad shell text search for code navigation.
- Use ordinary shell commands for non-search tasks such as running tests, checking git state, or listing a known directory.
- Treat the generated index as a navigation aid, not as proof. Open matched files before making behavioral claims or edits.
- If the repository already provides a stronger source database, tags database, LSP index, or project-specific SQLite schema, prefer that existing source and use this skill's query patterns against it where practical.
- If the script misses a symbol because of language syntax, use raw SQL against indexed `lines` or `files_fts` before falling back to text search.
- If query commands warn that rebuild is in progress, use the previous index result as a candidate and rebuild later when exact freshness matters.

## Commands

Common commands:

```bash
# Print the default database path for a root.
"$TOOL" path "$PWD"

# Initialize an empty schema when explicitly needed.
"$TOOL" init "$PWD"

# Atomic full rebuild. Current CLI skips successfully if another rebuild holds the lock.
"$TOOL" rebuild "$PWD"

# Show index status and lock state.
"$TOOL" status --root "$PWD"

# Find symbols.
"$TOOL" defs --root "$PWD" UserRepository

# Find files by path.
"$TOOL" files --root "$PWD" repository

# Show source from indexed lines.
"$TOOL" show --root "$PWD" --line 42 lib/user_repository.rb
```

The default database lives under `CODE_INDEX_CACHE_DIR` when set. Otherwise it uses `$XDG_CACHE_HOME/code-index` or `~/.cache/code-index`, keyed by the repository root path. Pass `--db path/to/index.sqlite` when a stable or shared index is needed.

## References

Read `references/query-patterns.md` when raw SQL is needed, when the built-in `defs` or `files` commands are not enough, or when adapting this workflow to another code index.
