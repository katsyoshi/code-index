select tables.name as table_name,
       columns.cid + 1 as ordinal,
       columns.name as column_name,
       case when columns.type = '' then '-' else columns.type end as type,
       case when columns."notnull" != 0 or columns.pk != 0 then 'no' else 'yes' end as nullable,
       case when columns.pk != 0 then 'primary(' || columns.pk || ')' else '-' end as key
from pragma_table_list as tables
join pragma_table_info(tables.name) as columns
where tables.schema = 'main'
  and tables.type in ('table', 'virtual')
  and tables.name not like 'sqlite_%'
order by tables.name, columns.cid;
