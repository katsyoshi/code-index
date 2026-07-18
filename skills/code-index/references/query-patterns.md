# Query Patterns

## Schema

Run `code-index schema --root "$PWD"` to inspect the tables and columns in the current database. The reference below describes the expected current schema.

`files`

- `id`: integer primary key
- `path`: repository-relative path
- `language`: detected language, nullable
- `extension`: file extension
- `size`: byte size
- `mtime`: filesystem modification time
- `content_hash`: SHA-256 content digest

`symbols`

- `id`: integer primary key
- `file_id`: `files.id`
- `path`: repository-relative path
- `language`: detected language
- `kind`: `function`, `method`, `class`, `module`, `type`, `trait`, `enum`, `interface`, `constant`, or `section`
- `name`: extracted symbol name
- `line`: 1-based line number
- `column`: 1-based column number
- `signature`: matched definition line
- `context`: nearby source lines

`lines`

- `file_id`: `files.id`
- `line`: 1-based line number
- `text`: line text

`file_metrics`

- `file_id`: `files.id`
- `path`: repository-relative path
- `language`: detected language, nullable
- `line_count`: total indexed lines
- `blank_lines`: blank lines
- `comment_lines`: likely comment lines
- `code_lines`: likely code lines
- `symbol_count`: extracted symbol count for the file

`files_fts`

- FTS5 table with `path`, `language`, and full file `content`
- Available only when the installed `sqlite3` command supports FTS5

`symbols_fts`

- FTS5 table with `name`, `kind`, `language`, `path`, `signature`, and `context`
- Available only when the installed `sqlite3` command supports FTS5

## Definition Lookup

Find exact symbol names:

```sql
select path, line, kind, name, signature
from symbols
where name = 'parse_config' collate nocase
order by path, line;
```

Find similarly named methods:

```sql
select path, line, kind, name, signature
from symbols
where name like '%config%' collate nocase
order by
  case
    when name = 'config' collate nocase then 0
    when name like 'config%' collate nocase then 1
    else 2
  end,
  path,
  line
limit 50;
```

Restrict to a language in mixed-language repositories:

```sql
select path, line, kind, name, signature
from symbols
where language = 'ruby'
  and name like '%route%' collate nocase
order by path, line;
```

When language detection is ambiguous, combine `language`, `extension`, and `path` predicates.

## File Lookup

Find paths by name:

```sql
select path, language, size
from files
where path like '%controller%' collate nocase
order by path
limit 100;
```

Find files of one language:

```sql
select path
from files
where language = 'python'
order by path;
```

## Source Text Lookup

Prefer FTS when available:

```sql
select path
from files_fts
where files_fts match 'timeout'
limit 50;
```

Fallback to indexed lines:

```sql
select files.path, lines.line, lines.text
from lines
join files on files.id = lines.file_id
where lines.text like '%timeout%' collate nocase
order by files.path, lines.line
limit 100;
```

## Metrics Lookup

Summarize indexed languages:

```sql
select language,
       count(*) as files,
       sum(line_count) as lines,
       sum(code_lines) as code,
       sum(comment_lines) as comments,
       sum(symbol_count) as symbols
from file_metrics
group by language
order by code desc, files desc;
```

Find large files with few extracted symbols:

```sql
select path, language, line_count, code_lines, symbol_count
from file_metrics
where code_lines >= 300
  and symbol_count <= 2
order by code_lines desc
limit 50;
```

## Call-Site Or Reference Lookup

This index is regex-based and does not build a typed call graph. For likely call sites, query indexed lines and exclude definition-like rows:

```sql
select files.path, lines.line, lines.text
from lines
join files on files.id = lines.file_id
where lines.text like '%parse_config%' collate nocase
  and lines.text not like '%def parse_config%'
  and lines.text not like '%function parse_config%'
order by files.path, lines.line
limit 100;
```

Open candidate files before relying on these rows for code changes.
