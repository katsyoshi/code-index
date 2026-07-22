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

	codesymbols "github.com/katsyoshi/code-index/internal/symbols"
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
	symbols     []codesymbols.Symbol
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
	symbols := codesymbols.Extract(rel, language, lines)
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
	quotedPath := quote(path)
	if fts {
		writeSQL(w, "%s\n", formatEmbeddedSQL("file_index_delete_fts.sql", quotedPath, quotedPath))
	}
	writeSQL(w, "%s\n", formatEmbeddedSQL("file_index_delete.sql", quotedPath, quotedPath, quotedPath, quotedPath))
}

func writeFileIndexInsertSQL(w io.Writer, index fileIndex, fts bool, fileID int, symbolID *int) {
	quotedPath := quote(index.path)
	quotedLanguage := nullableQuote(index.language)
	quotedExtension := quote(index.extension)
	quotedContentHash := quote(index.contentHash)
	fileIDExpr := "(select id from files where path = " + quotedPath + ")"
	if fileID > 0 {
		writeSQL(
			w,
			"%s\n",
			formatEmbeddedSQL(
				"file_insert_with_id.sql",
				fileID,
				quotedPath,
				quotedLanguage,
				quotedExtension,
				index.size,
				index.mtime,
				quotedContentHash,
			),
		)
		fileIDExpr = strconv.Itoa(fileID)
	} else {
		writeSQL(
			w,
			"%s\n",
			formatEmbeddedSQL(
				"file_insert.sql",
				quotedPath,
				quotedLanguage,
				quotedExtension,
				index.size,
				index.mtime,
				quotedContentHash,
			),
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
				"insert into symbols(id, file_id, path, language, kind, name, line, end_line, column, signature, context) values(%d, %s, %s, %s, %s, %s, %d, %d, %d, %s, %s);\n",
				*symbolID,
				fileIDExpr,
				quote(sym.Path),
				nullableQuote(sym.Language),
				quote(sym.Kind),
				quote(sym.Name),
				sym.Line,
				sym.EndLine,
				sym.Column,
				quote(sym.Signature),
				quote(sym.Context),
			)
			*symbolID = *symbolID + 1
		} else {
			writeSQL(
				w,
				"insert into symbols(file_id, path, language, kind, name, line, end_line, column, signature, context) values(%s, %s, %s, %s, %s, %d, %d, %d, %s, %s);\n",
				fileIDExpr,
				quote(sym.Path),
				nullableQuote(sym.Language),
				quote(sym.Kind),
				quote(sym.Name),
				sym.Line,
				sym.EndLine,
				sym.Column,
				quote(sym.Signature),
				quote(sym.Context),
			)
		}
		if fts {
			writeSQL(
				w,
				"insert into symbols_fts(name, kind, language, path, signature, context) values(%s, %s, %s, %s, %s, %s);\n",
				quote(sym.Name),
				quote(sym.Kind),
				nullableQuote(sym.Language),
				quote(sym.Path),
				quote(sym.Signature),
				quote(sym.Context),
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
