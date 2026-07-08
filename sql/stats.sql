select 'root' as key, value from meta where key = 'root'
union all select 'schema_version', value from meta where key = 'schema_version'
union all select 'file_source', value from meta where key = 'file_source'
union all select 'indexed_at', value from meta where key = 'indexed_at'
union all select 'updated_at', value from meta where key = 'updated_at'
union all select 'last_operation', value from meta where key = 'last_operation'
union all select 'vcs_kind', value from meta where key = 'vcs_kind'
union all select 'vcs_revision', value from meta where key = 'vcs_revision'
union all select 'vcs_ref', value from meta where key = 'vcs_ref'
union all select 'vcs_head', value from meta where key = 'vcs_head'
union all select 'vcs_branch', value from meta where key = 'vcs_branch'
union all select 'vcs_dirty', value from meta where key = 'vcs_dirty'
union all select 'files', cast(count(*) as text) from files
union all select 'symbols', cast(count(*) as text) from symbols
union all select 'lines', cast(count(*) as text) from lines
union all select 'code_lines', cast(coalesce(sum(code_lines), 0) as text) from file_metrics
union all select 'comment_lines', cast(coalesce(sum(comment_lines), 0) as text) from file_metrics
union all select 'blank_lines', cast(coalesce(sum(blank_lines), 0) as text) from file_metrics
union all select 'hash_algorithm', value from meta where key = 'hash_algorithm'
union all select 'fts5', value from meta where key = 'fts5';
