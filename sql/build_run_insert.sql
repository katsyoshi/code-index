begin immediate;
insert into build_runs(
  operation,
  status,
  root,
  db,
  started_at,
  finished_at,
  error_message
)
values (%s, %s, %s, %s, %s, %s, %s);

delete from build_runs
where id not in (
  select id from build_runs order by id desc limit %d
);
commit;
