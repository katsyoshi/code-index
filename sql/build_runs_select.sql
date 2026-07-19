select id,
       operation,
       status,
       root,
       db,
       started_at,
       finished_at,
       error_message as error
from build_runs
order by id desc
limit %d;
