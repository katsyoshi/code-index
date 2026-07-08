select coalesce(language, '(unknown)') as language,
       count(*) as files,
       sum(line_count) as lines,
       sum(code_lines) as code,
       sum(comment_lines) as comments,
       sum(blank_lines) as blank,
       sum(symbol_count) as symbols
from file_metrics
where %s
group by coalesce(language, '(unknown)')
order by lines desc, language
limit %d;
