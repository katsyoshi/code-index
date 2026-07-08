select path,
       language,
       line_count as lines,
       code_lines as code,
       comment_lines as comments,
       blank_lines as blank,
       symbol_count as symbols
from file_metrics
where %s
order by path
limit %d;
