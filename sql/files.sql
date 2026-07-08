select path, language, size
from files
where %s
order by path
limit %d;
