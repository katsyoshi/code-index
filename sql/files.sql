select path, language, size, index_status, source_encoding, encoding_source, transcoded, skip_reason
from files
where %s
order by path
limit %d;
