select path, line, kind, name, language, signature
from symbols
where %s
order by path, line, column, name
limit %d;
