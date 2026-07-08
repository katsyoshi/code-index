with target as (
  select id, path
  from files
  where path = %s or path like %s
  order by case when path = %s then 0 else 1 end, length(path)
  limit 1
)
select target.path as path, lines.line as line, lines.text as text
from lines join target on target.id = lines.file_id
where lines.line between %d and %d
order by lines.line;
