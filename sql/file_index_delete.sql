delete from lines where file_id in (select id from files where path = %s);
delete from symbols where path = %s;
delete from file_metrics where path = %s;
delete from files where path = %s;
