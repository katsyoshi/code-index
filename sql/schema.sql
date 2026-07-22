create table meta (
  key text primary key,
  value text not null
);
create table components (
  name text primary key,
  status text not null check (status in ('ready', 'disabled', 'unavailable')),
  updated_at text not null
);
create table files (
  id integer primary key,
  path text not null unique,
  language text,
  extension text,
  size integer not null,
  mtime integer not null,
  content_hash text not null,
  index_status text not null check (index_status in ('indexed', 'skipped')),
  source_encoding text,
  encoding_source text check (encoding_source in ('utf8', 'bom', 'declaration', 'fallback')),
  transcoded integer not null check (transcoded in (0, 1)),
  skip_reason text check (skip_reason in ('encoding_unknown', 'encoding_conflict', 'conversion_failed', 'transcoder_unavailable')),
  check ((index_status = 'indexed' and skip_reason is null) or (index_status = 'skipped' and skip_reason is not null))
);
create table symbols (
  id integer primary key,
  file_id integer not null references files(id) on delete cascade,
  path text not null,
  language text,
  kind text not null,
  name text not null,
  line integer not null,
  end_line integer not null,
  column integer not null,
  signature text not null,
  context text not null
);
create table lines (
  file_id integer not null references files(id) on delete cascade,
  line integer not null,
  text text not null,
  primary key (file_id, line)
);
create table file_metrics (
  file_id integer primary key references files(id) on delete cascade,
  path text not null unique,
  language text,
  line_count integer not null,
  blank_lines integer not null,
  comment_lines integer not null,
  code_lines integer not null,
  symbol_count integer not null
);
create index idx_files_path on files(path);
create index idx_files_language on files(language);
create index idx_files_index_status on files(index_status);
create index idx_symbols_name on symbols(name);
create index idx_symbols_path_line on symbols(path, line);
create index idx_symbols_language_kind on symbols(language, kind);
create index idx_file_metrics_path on file_metrics(path);
create index idx_file_metrics_language on file_metrics(language);
