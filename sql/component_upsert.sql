insert into components(name, status, updated_at)
values (%s, %s, %s)
on conflict(name) do update set
  status = excluded.status,
  updated_at = excluded.updated_at;
