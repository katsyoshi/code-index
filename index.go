package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"
)

type fileIndex struct {
	path        string
	language    string
	extension   string
	size        int64
	mtime       int64
	contentHash string
	text        string
	lines       []string
	symbols     []symbol
	metrics     fileMetrics
}

type indexedFileState struct {
	contentHash string
	size        int64
	mtime       int64
}

func scanFileIndex(root, path string, info fs.FileInfo, maxBytes int64) (fileIndex, error) {
	text, err := readText(path, maxBytes)
	if err != nil {
		return fileIndex{}, err
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return fileIndex{}, err
	}
	rel = filepath.ToSlash(rel)
	language := detectLanguage(path)
	lines := splitLines(text)
	symbols := extractSymbols(rel, language, lines)
	metrics := computeFileMetrics(language, lines, len(symbols))
	return fileIndex{
		path:        rel,
		language:    language,
		extension:   strings.ToLower(filepath.Ext(path)),
		size:        info.Size(),
		mtime:       info.ModTime().Unix(),
		contentHash: contentHash(text),
		text:        text,
		lines:       lines,
		symbols:     symbols,
		metrics:     metrics,
	}, nil
}

func writeFileIndexDeleteSQL(w io.Writer, path string, fts bool) {
	if fts {
		writeSQL(w, "delete from files_fts where path = %s;\n", quote(path))
		writeSQL(w, "delete from symbols_fts where path = %s;\n", quote(path))
	}
	writeSQL(w, "delete from lines where file_id in (select id from files where path = %s);\n", quote(path))
	writeSQL(w, "delete from symbols where path = %s;\n", quote(path))
	writeSQL(w, "delete from file_metrics where path = %s;\n", quote(path))
	writeSQL(w, "delete from files where path = %s;\n", quote(path))
}

func writeFileIndexInsertSQL(w io.Writer, index fileIndex, fts bool, fileID int, symbolID *int) {
	fileIDExpr := "(select id from files where path = " + quote(index.path) + ")"
	if fileID > 0 {
		writeSQL(
			w,
			"insert into files(id, path, language, extension, size, mtime, content_hash) values(%d, %s, %s, %s, %d, %d, %s);\n",
			fileID,
			quote(index.path),
			nullableQuote(index.language),
			quote(index.extension),
			index.size,
			index.mtime,
			quote(index.contentHash),
		)
		fileIDExpr = strconv.Itoa(fileID)
	} else {
		writeSQL(
			w,
			"insert into files(path, language, extension, size, mtime, content_hash) values(%s, %s, %s, %d, %d, %s);\n",
			quote(index.path),
			nullableQuote(index.language),
			quote(index.extension),
			index.size,
			index.mtime,
			quote(index.contentHash),
		)
	}
	for lineIndex, line := range index.lines {
		writeSQL(w, "insert into lines(file_id, line, text) values(%s, %d, %s);\n", fileIDExpr, lineIndex+1, quote(line))
	}
	writeSQL(
		w,
		"insert into file_metrics(file_id, path, language, line_count, blank_lines, comment_lines, code_lines, symbol_count) values(%s, %s, %s, %d, %d, %d, %d, %d);\n",
		fileIDExpr,
		quote(index.path),
		nullableQuote(index.language),
		index.metrics.lineCount,
		index.metrics.blankLines,
		index.metrics.commentLines,
		index.metrics.codeLines,
		index.metrics.symbolCount,
	)
	for _, sym := range index.symbols {
		if symbolID != nil {
			writeSQL(
				w,
				"insert into symbols(id, file_id, path, language, kind, name, line, column, signature, context) values(%d, %s, %s, %s, %s, %s, %d, %d, %s, %s);\n",
				*symbolID,
				fileIDExpr,
				quote(sym.path),
				nullableQuote(sym.language),
				quote(sym.kind),
				quote(sym.name),
				sym.line,
				sym.column,
				quote(sym.signature),
				quote(sym.context),
			)
			*symbolID = *symbolID + 1
		} else {
			writeSQL(
				w,
				"insert into symbols(file_id, path, language, kind, name, line, column, signature, context) values(%s, %s, %s, %s, %s, %d, %d, %s, %s);\n",
				fileIDExpr,
				quote(sym.path),
				nullableQuote(sym.language),
				quote(sym.kind),
				quote(sym.name),
				sym.line,
				sym.column,
				quote(sym.signature),
				quote(sym.context),
			)
		}
		if fts {
			writeSQL(
				w,
				"insert into symbols_fts(name, kind, language, path, signature, context) values(%s, %s, %s, %s, %s, %s);\n",
				quote(sym.name),
				quote(sym.kind),
				nullableQuote(sym.language),
				quote(sym.path),
				quote(sym.signature),
				quote(sym.context),
			)
		}
	}
	if fts {
		writeSQL(w, "insert into files_fts(path, language, content) values(%s, %s, %s);\n", quote(index.path), nullableQuote(index.language), quote(index.text))
	}
}

func readText(path string, maxBytes int64) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if int64(len(data)) > maxBytes || bytesContainNUL(data) {
		return "", errors.New("not text")
	}
	if !utf8.Valid(data) {
		return strings.ToValidUTF8(string(data), "?"), nil
	}
	return string(data), nil
}

func bytesContainNUL(data []byte) bool {
	limit := len(data)
	if limit > 8192 {
		limit = 8192
	}
	for _, b := range data[:limit] {
		if b == 0 {
			return true
		}
	}
	return false
}

func detectLanguage(path string) string {
	base := filepath.Base(path)
	if lang, ok := langByName[base]; ok {
		return lang
	}
	return langByExt[strings.ToLower(filepath.Ext(path))]
}

func splitLines(text string) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	if text == "" {
		return nil
	}
	lines := strings.Split(text, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func contentHash(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}
