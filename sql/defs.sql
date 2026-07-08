select path, line, kind, name, language, signature
from symbols
where %s
order by
  case
    when name = %s collate nocase then 0
    when name like %s collate nocase then 1
    else 2
  end,
  path,
  line
limit %d;
