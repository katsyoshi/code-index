---
name: code-index
description: Build and use a local SQLite index for code navigation instead of ad hoc grep/rg/find searches. Use when an AI coding agent needs to locate files, classes, functions, methods, symbols, definitions, references, source lines, code metrics, or index status across an unfamiliar repository; when repeated codebase lookup would otherwise use shell text search; or when the user asks for SQL-backed, database-backed, or indexed code search.
---

# Code Index

## Overview

Use `code-index` as the first search surface for codebase navigation. Prefer SQL-backed queries for locating files, definitions, methods, classes, relevant source lines, metrics, and index status.

Use an external `code-index` binary from an explicit `CODE_INDEX_BIN` path. `code-index` may be on `PATH` for humans, but this skill must not search `PATH`. If `CODE_INDEX_BIN` is not set, ask the user to install `code-index` and configure `CODE_INDEX_BIN` in the environment used by their agent runtime.

## Install Guidance

When `CODE_INDEX_BIN` is missing or points to a non-executable file, do not search `PATH`. Ask the user to install `code-index` under the directory containing this `SKILL.md`, then configure `CODE_INDEX_BIN` in the environment used by their agent runtime:

```bash
SKILL_DIR=/path/to/installed/skills/code-index
mkdir -p "$SKILL_DIR/exec"
GOBIN="$SKILL_DIR/exec" go install github.com/katsyoshi/code-index@latest
export CODE_INDEX_BIN="$SKILL_DIR/exec/code-index"
"$CODE_INDEX_BIN" version
```

For local development of this repository, building the checked-out source with `go build -o "$SKILL_DIR/exec/code-index" .` and pointing `CODE_INDEX_BIN` at that binary is also acceptable.

The `version` output identifies the binary by build commit when available. Treat the commit hash as an identity, not as an ordered version, unless you have commit-history context.

## Operating Principles

Read the `Design` section in the repository `README.md` as the source of truth for project direction. In this skill, apply that direction as an operating rule: use `code-index` to reduce how much source enters the LLM context.

- Query SQLite/FTS before opening files broadly.
- Prefer a few targeted `show`, `defs`, `files`, `metrics`, or read-only SQL results over loading whole directories.
- Treat indexed matches as navigation candidates and open source before making behavioral claims.

## Workflow

1. Resolve the target repository root.
2. Select the tool:

```bash
if [ -z "${CODE_INDEX_BIN:-}" ]; then
  echo "CODE_INDEX_BIN is not set; install code-index and set CODE_INDEX_BIN to the binary path" >&2
  exit 1
fi

TOOL="$CODE_INDEX_BIN"

if [ ! -x "$TOOL" ]; then
  echo "code-index is not executable: $TOOL" >&2
  exit 1
fi
```

3. Check the tool build information. Prefer a build commit hash over semver for identifying the binary, but do not infer ordering from the hash without commit history. Treat the hash as compatible only when it is in a known compatible list, or use explicit feature checks when the binary supports them. If the command is unsupported or the build is incompatible for the workflow you need, ask the user to install or configure a compatible `code-index` binary:

```bash
"$TOOL" version --format json
```

4. In sandboxed agent sessions, prefer a writable cache directory unless the user gave a DB path:

```bash
export CODE_INDEX_CACHE_DIR="${CODE_INDEX_CACHE_DIR:-/tmp/code-index}"
```

5. Build or refresh the index before substantial search work. Prefer `update` for an existing index; it refreshes changed Git-tracked files and removes files no longer tracked by Git:

```bash
"$TOOL" update --format json
```

If `update` reports that the index does not exist yet, run `rebuild` explicitly:

```bash
"$TOOL" rebuild --format json
```

If `update` reports incompatible schema, file source, hash, or indexing config settings, run `rebuild`. If it reports another checkout path or unknown Git history, run `rebuild` unless the user explicitly wants this DB to belong to the current checkout; only then use `update --adopt`.

6. Check status when lock or freshness may matter:

```bash
"$TOOL" status --format json
```

Use `components`, `update_compatible`, `update_requires_adopt`, `update_rebuild_required`, and `update_blocker` from `status` to confirm the completed index state and decide whether to run normal `update`, ask before `update --adopt`, or run `rebuild`.

If `status` is unsupported, continue with query commands and rely on rebuild output.

7. Search definitions and files through the index:

```bash
"$TOOL" defs --format json parse_config
"$TOOL" files --format json config
```

8. Run raw read-only SQL for precise lookup:

```bash
"$TOOL" sql --format json \
  "select path, line, kind, name, signature from symbols where name like '%parse%' order by path, line limit 50"
```

9. Show source around indexed lines:

```bash
"$TOOL" show --line 42 --format json lib/config.rb
```

10. Run `update` after editing tracked files that affect search results. Use `rebuild` after tool upgrades, schema changes, or option changes that should refresh every tracked file.

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
# Show command help.
"$TOOL" help
"$TOOL" help update
"$TOOL" help --format json
"$TOOL" help --format json update

# Print the default database path for a root.
"$TOOL" path --format json

# Show build information for compatibility checks.
"$TOOL" version --format json

# Initialize an empty schema when explicitly needed.
"$TOOL" init --format json

# Atomic full rebuild from Git-tracked files. Current CLI skips successfully if another operation holds the lock.
"$TOOL" rebuild --format json

# Incrementally refresh an existing index from Git-tracked files.
"$TOOL" update --format json

# Adopt an index from another checkout path or Git history only when intentional.
"$TOOL" update --adopt --format json

# Show lock state and metadata from the last successful update in the agent-oriented format.
"$TOOL" status --format json

# Find symbols.
"$TOOL" defs --list --format json
"$TOOL" defs --format json UserRepository

# Find files by path.
"$TOOL" files --list --format json
"$TOOL" files --format json repository

# Inspect the available tables and columns in the agent-oriented format.
"$TOOL" schema --format json

# Show index-wide counts and build metadata.
"$TOOL" stats --format json

# Show source from indexed lines.
"$TOOL" show --line 42 --format json lib/user_repository.rb
```

Commands run inside a Git work tree discover its repository root automatically. The default database lives under `CODE_INDEX_CACHE_DIR` when set. Otherwise it uses `$XDG_CACHE_HOME/code-index` or `~/.cache/code-index`, keyed by the repository root path. A project `.code-index.toml` may override the database with a repository-relative `db`; pass `--db path/to/index.sqlite` for an explicit one-run override.

## References

Read `references/query-patterns.md` when raw SQL is needed, when the built-in `defs` or `files` commands are not enough, or when adapting this workflow to another code index.
