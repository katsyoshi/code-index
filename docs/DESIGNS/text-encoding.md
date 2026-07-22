# Source text encoding design

## Purpose

`code-index` stores and searches source text as UTF-8 regardless of the source
file's original encoding. Source files are read-only inputs and are never
rewritten during `rebuild` or `update`.

The conversion layer applies to every feature that consumes source text, not
only a particular query command. This includes indexed lines, full-text search,
symbols, signatures, context, metrics, `show`, `outline`, `defs`, and `refs`.

## Principles

- Text written to SQLite must be valid UTF-8.
- Conversion must not silently replace undecodable bytes with `?` or the
  Unicode replacement character.
- A source encoding must be known before a general-purpose transcoder is used.
  `iconv` converts encodings but is not treated as a reliable encoding detector.
- Files whose encoding cannot be determined or converted are skipped with a
  diagnostic. One such file must not prevent other tracked files from being
  indexed.
- The index remains lightweight and rebuildable. It does not preserve an
  additional byte-for-byte copy of each source file.

## Decoding order

Each Git-tracked file that passes the size limit is decoded in this order:

1. A Unicode BOM selects UTF-8, UTF-16LE, UTF-16BE, UTF-32LE, or UTF-32BE. These
   encodings are decoded internally without requiring an external command.
2. A supported language-level encoding declaration selects an explicit source
   encoding. Ruby magic comments and Python encoding declarations are the
   initial declaration forms to support.
3. A valid UTF-8 byte sequence is used directly.
4. The project's ordered `encoding.fallbacks` setting supplies fallback
   encodings. Each configured encoding is attempted in order, and the first
   successful conversion to valid UTF-8 is used.
5. If no encoding is known, the file is skipped. Automatic guessing by `iconv`
   or locale-dependent fallback is not permitted.

The NUL-byte binary-file check runs after BOM recognition so that UTF-16 and
UTF-32 text is not rejected as binary data. Files without a recognized Unicode
BOM remain subject to the binary-file check before any external conversion.

A BOM and a language declaration are both explicit. When they disagree, the
file is stored as skipped with `encoding_conflict`; neither declaration wins.

## External conversion

`iconv` is an optional transcoder for explicitly named non-Unicode source
encodings such as Windows-31J, EUC-JP, Windows-1252, or ISO-8859 variants.

The command is invoked without a shell, receives the source bytes on standard
input, and must produce valid UTF-8 on standard output. A missing command, a
non-zero exit status, or invalid UTF-8 output makes conversion fail for that
file. Partial output is discarded.

The initial implementation does not require `iconv` to index valid UTF-8 or
BOM-marked Unicode files. A checkout containing only those files therefore has
no external runtime dependency.

`nkf` is not part of the initial conversion contract. It may later be added as
an explicitly selected Japanese-encoding adapter, but it must not change the
deterministic decoding order or become an implicit detector for every invalid
UTF-8 file.

## Project configuration

The project configuration may provide an ordered list of fallback source
encodings:

```toml
[encoding]
fallbacks = ["Windows-31J", "EUC-JP"]
```

The default is an empty list. A Unicode BOM, a supported language declaration,
and valid UTF-8 take precedence over this setting. Consequently,
`encoding.fallbacks` does not force UTF-8 files through `iconv`.

The list order is significant and belongs to the user-visible configuration
contract. Successful conversion proves only that the input was valid for that
decoder; it does not prove that the selected encoding was semantically correct.
This is especially important for permissive single-byte encodings. Users should
therefore place the expected project encoding first and include alternatives
only when the repository actually needs them.

Empty names and case-insensitive duplicates make configuration invalid.
Non-Unicode names are passed to the platform's `iconv`; unsupported names are
therefore reported as conversion failures rather than rejected while parsing
configuration. Path-specific encoding rules are not supported initially.

## Language declarations

Encoding declarations are inspected as ASCII-compatible byte syntax before the
whole file is decoded. A declaration must map to a recognized canonical
encoding name before it is passed to a transcoder.

The declaration belongs to the source language, not to the filename alone.
Language-specific rules define where a declaration is legal and how aliases are
normalized. Conflicting, malformed, or unsupported declarations cause the file
to be skipped with a diagnostic rather than decoded heuristically.

Declaration support can be added per language. The first intended cases are:

- Ruby magic comments such as `# encoding: Windows-31J`
- Python declarations described by the language's source-encoding syntax

## Size, hashes, and line endings

`max_bytes` applies to the original source byte length before conversion. This
prevents a small encoded input from expanding beyond the configured read limit
without accounting for its source size.

File identity and incremental-update hashes are calculated from the original
bytes. Transcoding policy is part of the indexing configuration identity, so a
change to that policy requires affected files to be re-indexed or the index to
be rebuilt.

Line-ending normalization occurs after decoding. CRLF and CR are normalized to
LF for indexed lines, matching the existing text-indexing behavior. Source files
are not modified.

## Stored metadata

Every successfully indexed or encoding-skipped file records enough information
to explain how its text was obtained:

- the canonical Unicode encoding or selected `iconv` label, such as `UTF-8`,
  `UTF-16LE`, or `Windows-31J`
- whether transcoding was performed
- the origin of the encoding decision: UTF-8 validation, BOM, language
  declaration, or project configuration
- `index_status`, either `indexed` or `skipped`
- a stable skip reason when `index_status = skipped`

Skipped files have no rows in `lines`, FTS, `symbols`, or `file_metrics`. Their
path, raw size, raw content hash, language, and encoding diagnostic remain in
`files`. This metadata must not be interpreted as a promise that automatic
detection occurred.

## Diagnostics and command output

`rebuild` and `update` continue indexing when an individual file cannot be
decoded. Normal text and JSON output include `transcoded_files` and
`encoding_skipped_files`. With `-v` or `--verbose`, text output lists each
encoding skip and JSON output adds a `diagnostics` array.

`files` defaults to `--status indexed`, preserving the normal indexed-file
view. `--status skipped` returns encoding-skipped files and their reasons, while
`--status all` returns both groups.

An encoding skip is distinct from a file exceeding `max_bytes` or being binary,
even if all three cases omit source text from the index.

## Initial implementation scope

The first implementation should include:

- valid UTF-8, with optional BOM removal
- BOM-marked UTF-16LE, UTF-16BE, UTF-32LE, and UTF-32BE decoded internally
- Ruby and Python encoding declarations
- ordered `encoding.fallbacks` configuration
- optional `iconv` conversion when a declaration names a supported non-Unicode
  encoding or a configured fallback is attempted
- lossless skipping and diagnostics for unknown or failed encodings

Project path rules, `nkf`, and heuristic encoding detection are deferred.

## Non-goals

- Rewriting source files as UTF-8
- Guessing arbitrary legacy encodings from byte frequency
- Supporting different fallback lists for different paths
- Recovering files that mix encodings within one file
- Depending on the process locale for decoding
- Treating every NUL-containing file as text
- Preserving original source bytes inside SQLite
- Making `code-index` a general-purpose encoding conversion tool
- Guaranteeing that every encoding accepted by one platform's `iconv` exists
  on every other platform
