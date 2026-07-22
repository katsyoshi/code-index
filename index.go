package main

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	codesymbols "github.com/katsyoshi/code-index/internal/symbols"
)

type fileIndex struct {
	path           string
	language       string
	extension      string
	size           int64
	mtime          int64
	contentHash    string
	indexStatus    string
	sourceEncoding string
	encodingSource string
	transcoded     bool
	skipReason     string
	text           string
	lines          []string
	symbols        []codesymbols.Symbol
	metrics        fileMetrics
}

type indexedFileState struct {
	contentHash string
	size        int64
	mtime       int64
	indexStatus string
}

func scanFileIndex(root, path string, info fs.FileInfo, config buildConfig) (fileIndex, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return fileIndex{}, err
	}
	if int64(len(data)) > config.maxBytes {
		return fileIndex{}, errNotText
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return fileIndex{}, err
	}
	rel = filepath.ToSlash(rel)
	language := detectLanguage(path)
	decoded, err := decodeSource(data, language, config.encodingFallbacks)
	if err != nil {
		return fileIndex{}, err
	}
	index := fileIndex{
		path: rel, language: language, extension: strings.ToLower(filepath.Ext(path)),
		size: info.Size(), mtime: info.ModTime().Unix(), contentHash: contentHash(data),
		indexStatus: decoded.indexStatus, sourceEncoding: decoded.sourceEncoding,
		encodingSource: decoded.encodingSource, transcoded: decoded.transcoded, skipReason: decoded.skipReason,
	}
	if decoded.indexStatus == indexStatusSkipped {
		return index, nil
	}
	text := decoded.text
	lines := splitLines(text)
	symbols := codesymbols.Extract(rel, language, lines)
	metrics := computeFileMetrics(language, lines, len(symbols))
	index.text = text
	index.lines = lines
	index.symbols = symbols
	index.metrics = metrics
	return index, nil
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
	quotedIndexStatus := quote(index.indexStatus)
	quotedSourceEncoding := nullableQuote(index.sourceEncoding)
	quotedEncodingSource := nullableQuote(index.encodingSource)
	quotedSkipReason := nullableQuote(index.skipReason)
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
				quotedIndexStatus,
				quotedSourceEncoding,
				quotedEncodingSource,
				boolInt(index.transcoded),
				quotedSkipReason,
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
				quotedIndexStatus,
				quotedSourceEncoding,
				quotedEncodingSource,
				boolInt(index.transcoded),
				quotedSkipReason,
			),
		)
	}
	if index.indexStatus != indexStatusIndexed {
		return
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

func contentHash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
