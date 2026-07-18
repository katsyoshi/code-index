select tables.name,
       columns.cid + 1,
       columns.name,
       case when columns.type = '' then '-' else columns.type end,
       case when columns."notnull" != 0 or columns.pk != 0 then 0 else 1 end,
       case when columns.pk != 0 then 'primary(' || columns.pk || ')' else null end
from pragma_table_list as tables
join pragma_table_info(tables.name) as columns
where tables.schema = 'main'
  and tables.type in ('table', 'virtual')
  and tables.name not like 'sqlite_%'
order by tables.name, columns.cid;
