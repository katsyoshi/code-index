with target as (
  select id, path
  from files
  where path = %s or path like %s
  order by case when path = %s then 0 else 1 end, length(path)
  limit 1
)
select target.path as path,
       symbols.line as line,
       symbols.kind as kind,
       symbols.name as name,
       symbols.language as language,
       symbols.signature as signature
from symbols join target on target.id = symbols.file_id
order by symbols.line, symbols.column, symbols.name;
