select count(*)
from sqlite_master
where type = 'table'
  and name in ('files_fts', 'symbols_fts');
