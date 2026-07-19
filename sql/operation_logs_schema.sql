pragma busy_timeout = 5000;
create table if not exists build_runs (
  id integer primary key,
  operation text not null,
  status text not null check (status in ('succeeded', 'failed', 'skipped')),
  root text not null,
  db text not null,
  started_at text not null,
  finished_at text not null,
  error_message text
);
