# AGENTS.md

This file is the shared repository guidance for `code-index`. It should contain
project conventions that are useful to every contributor.

If `.local/AGENTS.md` exists at the repository root, read it after this file.
Use it for personal workflow preferences, local paths, machine-specific
commands, or other per-user notes. Do not commit files under `.local/`.

This repository contains `code-index`, a small Go CLI that builds a local
SQLite index for source-code navigation. Keep changes focused on preserving a
lightweight, rebuildable index for agents rather than turning the project into
a language server or full code-intelligence system.

## Development

- Prefer small, behavior-preserving commits.
- Keep implementation style close to the existing Go code.
- Use `gofmt` on edited Go files.
- Do not rewrite unrelated code while making narrow changes.
- The checked-in SQL under `sql/` is embedded into the binary with `go:embed`.
  Prefer adding or changing SQL files over growing large SQL string literals in
  Go source.
- Keep dynamic SQL concerns in Go when values, filters, limits, or feature
  flags are assembled at runtime. Quote values explicitly with the existing
  helpers.
- `rebuild` is the atomic full rebuild path. `update` is the incremental
  refresh path. Preserve that division.
- Indexing should continue to use Git-tracked files only unless a task
  explicitly changes that behavior.

## Verification

Run the focused checks that match the change. For normal code changes, use:

```sh
gofmt -w <edited-go-files>
go test ./...
go vet ./...
```

For CLI behavior changes, also run a small smoke test with a temporary DB, for
example:

```sh
go run . rebuild --db /tmp/code-index-smoke.sqlite .
go run . stats --db /tmp/code-index-smoke.sqlite
go run . metrics --db /tmp/code-index-smoke.sqlite
```

When changing `update`, include a smoke path that exercises incremental refresh
or rely on the existing tests that cover changed, added, and deleted tracked
files.

## Repository Notes

- `README.md` is the user-facing documentation.
- `skills/code-index/SKILL.md` is the reusable agent skill source.
- The root `code-index` binary is ignored and may be stale. Prefer `go run .`
  or rebuild it explicitly with `go build -o code-index .`.
- Generated SQLite databases and sidecar files are ignored.
