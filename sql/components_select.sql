select name, status, updated_at
from components
order by case name
  when 'files' then 1
  when 'lines' then 2
  when 'symbols' then 3
  when 'metrics' then 4
  when 'fts' then 5
  else 100
end, name;
