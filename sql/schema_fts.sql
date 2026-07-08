create virtual table files_fts using fts5(path, language, content);
create virtual table symbols_fts using fts5(name, kind, language, path, signature, context);
