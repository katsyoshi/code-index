package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

var ignoredDirs = map[string]bool{
	".bundle":        true,
	".cache":         true,
	".git":           true,
	".gradle":        true,
	".hg":            true,
	".idea":          true,
	".mypy_cache":    true,
	".pytest_cache":  true,
	".ruff_cache":    true,
	".svn":           true,
	".terraform":     true,
	".venv":          true,
	".vscode":        true,
	"__pycache__":    true,
	"bazel-bin":      true,
	"bazel-out":      true,
	"bazel-testlogs": true,
	"build":          true,
	"coverage":       true,
	"deps":           true,
	"dist":           true,
	"node_modules":   true,
	"out":            true,
	"pkg":            true,
	"target":         true,
	"tmp":            true,
	"vendor":         true,
	"venv":           true,
}

var binaryExts = map[string]bool{
	".7z":      true,
	".a":       true,
	".bin":     true,
	".bmp":     true,
	".bundle":  true,
	".class":   true,
	".dll":     true,
	".dylib":   true,
	".exe":     true,
	".gif":     true,
	".ico":     true,
	".jar":     true,
	".jpeg":    true,
	".jpg":     true,
	".lock":    true,
	".mp3":     true,
	".mp4":     true,
	".o":       true,
	".pdf":     true,
	".png":     true,
	".pyc":     true,
	".rlib":    true,
	".so":      true,
	".sqlite":  true,
	".sqlite3": true,
	".wasm":    true,
	".wav":     true,
	".webp":    true,
	".zip":     true,
}

var langByExt = map[string]string{
	".bash":  "shell",
	".c":     "c",
	".cc":    "cpp",
	".cljs":  "clojure",
	".clj":   "clojure",
	".cpp":   "cpp",
	".cs":    "csharp",
	".css":   "css",
	".cxx":   "cpp",
	".el":    "elisp",
	".ex":    "elixir",
	".exs":   "elixir",
	".go":    "go",
	".h":     "c",
	".hpp":   "cpp",
	".hs":    "haskell",
	".html":  "html",
	".java":  "java",
	".js":    "javascript",
	".jsx":   "javascript",
	".kt":    "kotlin",
	".kts":   "kotlin",
	".lua":   "lua",
	".mjs":   "javascript",
	".php":   "php",
	".py":    "python",
	".rake":  "ruby",
	".rb":    "ruby",
	".rs":    "rust",
	".scala": "scala",
	".scm":   "scheme",
	".sh":    "shell",
	".swift": "swift",
	".ts":    "typescript",
	".tsx":   "typescript",
	".vue":   "vue",
	".zsh":   "shell",
}

var langByName = map[string]string{
	"Brewfile":   "ruby",
	"Dockerfile": "dockerfile",
	"Gemfile":    "ruby",
	"Justfile":   "make",
	"Makefile":   "make",
	"Rakefile":   "ruby",
}

type symbolSpec struct {
	re   *regexp.Regexp
	kind string
}

type symbol struct {
	path      string
	language  string
	kind      string
	name      string
	line      int
	column    int
	signature string
	context   string
}

type fileMetrics struct {
	lineCount    int
	blankLines   int
	commentLines int
	codeLines    int
	symbolCount  int
}

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

type indexLock struct {
	path string
}

type indexLockInfo struct {
	operation string
	root      string
	pid       string
	startedAt string
}

var symbolPatterns = map[string][]symbolSpec{
	"python": {
		spec(`^\s*(?:async\s+)?def\s+([A-Za-z_]\w*)\s*\(`, "function"),
		spec(`^\s*class\s+([A-Za-z_]\w*)\b`, "class"),
	},
	"ruby": {
		spec(`^\s*def\s+((?:self\.)?[A-Za-z_]\w*[!?=]?)\b`, "method"),
		spec(`^\s*class\s+([A-Za-z_:]\w*(?:::\w+)*)\b`, "class"),
		spec(`^\s*module\s+([A-Za-z_:]\w*(?:::\w+)*)\b`, "module"),
	},
	"javascript": {
		spec(`^\s*(?:export\s+)?(?:async\s+)?function\s+([$A-Za-z_][\w$]*)\s*\(`, "function"),
		spec(`^\s*(?:export\s+)?(?:const|let|var)\s+([$A-Za-z_][\w$]*)\s*=\s*(?:async\s*)?(?:\([^)]*\)|[$A-Za-z_][\w$]*)\s*=>`, "function"),
		spec(`^\s*(?:export\s+)?class\s+([$A-Za-z_][\w$]*)\b`, "class"),
		spec(`^\s*(?:static\s+|async\s+|get\s+|set\s+)*([$A-Za-z_][\w$]*)\s*\([^)]*\)\s*\{?\s*$`, "method"),
	},
	"typescript": {
		spec(`^\s*(?:export\s+)?(?:async\s+)?function\s+([$A-Za-z_][\w$]*)\s*\(`, "function"),
		spec(`^\s*(?:export\s+)?(?:const|let|var)\s+([$A-Za-z_][\w$]*)\s*[:=].*=>`, "function"),
		spec(`^\s*(?:export\s+)?(?:abstract\s+)?class\s+([$A-Za-z_][\w$]*)\b`, "class"),
		spec(`^\s*(?:export\s+)?interface\s+([$A-Za-z_][\w$]*)\b`, "interface"),
		spec(`^\s*(?:public\s+|private\s+|protected\s+|static\s+|async\s+|get\s+|set\s+)*([$A-Za-z_][\w$]*)\s*\([^)]*\)\s*[:{]`, "method"),
	},
	"go": {
		spec(`^\s*func\s+(?:\([^)]*\)\s*)?([A-Za-z_]\w*)\s*\(`, "function"),
		spec(`^\s*type\s+([A-Za-z_]\w*)\s+(?:struct|interface)\b`, "type"),
	},
	"rust": {
		spec(`^\s*(?:pub(?:\([^)]*\))?\s+)?(?:async\s+)?fn\s+([A-Za-z_]\w*)\s*[<(]`, "function"),
		spec(`^\s*(?:pub(?:\([^)]*\))?\s+)?struct\s+([A-Za-z_]\w*)\b`, "type"),
		spec(`^\s*(?:pub(?:\([^)]*\))?\s+)?enum\s+([A-Za-z_]\w*)\b`, "enum"),
		spec(`^\s*(?:pub(?:\([^)]*\))?\s+)?trait\s+([A-Za-z_]\w*)\b`, "trait"),
	},
	"java": {
		spec(`^\s*(?:public|protected|private|abstract|final|static|\s)+class\s+([A-Za-z_]\w*)\b`, "class"),
		spec(`^\s*(?:public|protected|private|static|final|synchronized|native|abstract|\s)+[\w<>\[\], ?]+\s+([A-Za-z_]\w*)\s*\([^;]*\)\s*\{?\s*$`, "method"),
	},
	"kotlin": {
		spec(`^\s*(?:public|private|protected|internal|open|override|suspend|\s)*fun\s+([A-Za-z_]\w*)\s*\(`, "function"),
		spec(`^\s*(?:data\s+|sealed\s+|open\s+)?class\s+([A-Za-z_]\w*)\b`, "class"),
		spec(`^\s*interface\s+([A-Za-z_]\w*)\b`, "interface"),
	},
	"swift": {
		spec(`^\s*(?:public|private|internal|open|static|class|mutating|\s)*func\s+([A-Za-z_]\w*)\s*\(`, "function"),
		spec(`^\s*(?:public|private|internal|open|\s)*(?:class|struct|enum|protocol)\s+([A-Za-z_]\w*)\b`, "type"),
	},
	"csharp": {
		spec(`^\s*(?:public|private|protected|internal|static|async|virtual|override|sealed|partial|\s)+class\s+([A-Za-z_]\w*)\b`, "class"),
		spec(`^\s*(?:public|private|protected|internal|static|async|virtual|override|sealed|\s)+[\w<>\[\], ?]+\s+([A-Za-z_]\w*)\s*\([^;]*\)\s*\{?\s*$`, "method"),
	},
	"php": {
		spec(`^\s*(?:public|protected|private|static|\s)*function\s+([A-Za-z_]\w*)\s*\(`, "function"),
		spec(`^\s*(?:abstract\s+|final\s+)?class\s+([A-Za-z_]\w*)\b`, "class"),
	},
	"elixir": {
		spec(`^\s*defmodule\s+([A-Za-z_]\w*(?:\.[A-Za-z_]\w*)*)\s+do\b`, "module"),
		spec(`^\s*defp?\s+([A-Za-z_]\w*[!?]?)\b`, "function"),
	},
	"lua": {
		spec(`^\s*(?:local\s+)?function\s+([A-Za-z_]\w*(?:[.:]\w+)*)\s*\(`, "function"),
		spec(`^\s*([A-Za-z_]\w*(?:[.:]\w+)*)\s*=\s*function\s*\(`, "function"),
	},
	"shell": {
		spec(`^\s*(?:function\s+)?([A-Za-z_][\w.-]*)\s*\(\)\s*\{?`, "function"),
		spec(`^\s*function\s+([A-Za-z_][\w.-]*)\b`, "function"),
	},
	"elisp": {
		spec(`^\s*\((?:cl-)?defun\s+([-A-Za-z0-9_+*/!?<>=]+)\b`, "function"),
		spec(`^\s*\(defmacro\s+([-A-Za-z0-9_+*/!?<>=]+)\b`, "function"),
		spec(`^\s*\(def(?:var|custom|const)\s+([-A-Za-z0-9_+*/!?<>=]+)\b`, "constant"),
	},
	"scheme": {
		spec(`^\s*\(define\s+\(?([-A-Za-z0-9_+*/!?<>=]+)\b`, "function"),
	},
	"clojure": {
		spec(`^\s*\(defn-?\s+([-A-Za-z0-9_+*/!?<>=]+)\b`, "function"),
		spec(`^\s*\(def(?:macro|record|protocol|multi)?\s+([-A-Za-z0-9_+*/!?<>=]+)\b`, "constant"),
	},
	"c": {
		spec(`^\s*(?:[A-Za-z_][\w\s*]+)\s+([A-Za-z_]\w*)\s*\([^;{}]*\)\s*(?:\{|$)`, "function"),
	},
	"cpp": {
		spec(`^\s*(?:template\s*<[^>]+>\s*)?(?:[\w:<>,~*&\s]+)\s+([A-Za-z_~]\w*)\s*\([^;{}]*\)\s*(?:const\s*)?(?:noexcept\s*)?(?:->\s*[^{]+)?\s*(?:\{|$)`, "function"),
		spec(`^\s*(?:class|struct)\s+([A-Za-z_]\w*)\b`, "type"),
	},
}

var skipSymbolNames = map[string]bool{
	"catch":  true,
	"else":   true,
	"for":    true,
	"if":     true,
	"switch": true,
	"while":  true,
	"with":   true,
}

var errIndexLocked = errors.New("index locked")

const contentHashAlgorithm = "sha256"

func spec(pattern, kind string) symbolSpec {
	return symbolSpec{re: regexp.MustCompile(pattern), kind: kind}
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		usage()
		return errors.New("missing command")
	}
	switch args[0] {
	case "path":
		return cmdPath(args[1:])
	case "init":
		return cmdInit(args[1:])
	case "rebuild":
		return cmdRebuild(args[1:])
	case "update":
		return cmdUpdate(args[1:])
	case "defs":
		return cmdDefs(args[1:])
	case "files":
		return cmdFiles(args[1:])
	case "sql":
		return cmdSQL(args[1:])
	case "show":
		return cmdShow(args[1:])
	case "stats":
		return cmdStats(args[1:])
	case "metrics":
		return cmdMetrics(args[1:])
	case "status":
		return cmdStatus(args[1:])
	default:
		usage()
		return fmt.Errorf("unknown command: %s", args[0])
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: code-index <path|init|rebuild|update|defs|files|sql|show|stats|metrics|status> [options]")
}

func cmdPath(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: code-index path ROOT")
	}
	root, err := filepath.Abs(args[0])
	if err != nil {
		return err
	}
	fmt.Println(defaultDBPath(root))
	return nil
}

func cmdInit(args []string) error {
	flags := flag.NewFlagSet("init", flag.ExitOnError)
	dbPath := flags.String("db", "", "database path")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return errors.New("usage: code-index init [--db DB] ROOT")
	}
	if _, err := exec.LookPath("sqlite3"); err != nil {
		return errors.New("sqlite3 command not found")
	}
	root, err := filepath.Abs(flags.Arg(0))
	if err != nil {
		return err
	}
	info, err := os.Stat(root)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("not a directory: %s", root)
	}
	db := *dbPath
	if db == "" {
		db = defaultDBPath(root)
	}
	if _, err := os.Stat(db); err == nil {
		return fmt.Errorf("index already exists: %s; run rebuild to replace it", db)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
		return err
	}
	lock, err := acquireIndexLock(db, "init", root)
	if err != nil {
		return err
	}
	defer lock.release()
	if _, err := os.Stat(db); err == nil {
		return fmt.Errorf("index already exists: %s; run rebuild to replace it", db)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	tmpDB, err := createTempDBPath(db)
	if err != nil {
		return err
	}
	installed := false
	defer func() {
		if !installed {
			_ = removeDBFiles(tmpDB)
		}
	}()
	fts := hasFTS5()
	writer, wait, err := sqliteWriter(tmpDB)
	if err != nil {
		return err
	}
	writerOK := false
	defer func() {
		if !writerOK {
			_ = writer.Close()
			_ = wait()
		}
	}()
	writeSchema(writer, fts)
	writeSQL(writer, "insert into meta(key, value) values(%s, %s);\n", quote("root"), quote(root))
	writeSQL(writer, "insert into meta(key, value) values(%s, %s);\n", quote("fts5"), quote(boolText(fts)))
	writeSQL(writer, "insert into meta(key, value) values(%s, %s);\n", quote("hash_algorithm"), quote(contentHashAlgorithm))
	writeSQL(writer, "commit;\n")
	if err := writer.Close(); err != nil {
		return err
	}
	if err := wait(); err != nil {
		return err
	}
	writerOK = true
	if err := installBuiltDB(tmpDB, db); err != nil {
		return err
	}
	installed = true
	fmt.Printf("db: %s\n", db)
	fmt.Printf("root: %s\n", root)
	fmt.Printf("files: 0\n")
	fmt.Printf("symbols: 0\n")
	fmt.Printf("lines: 0\n")
	fmt.Printf("code_lines: 0\n")
	fmt.Printf("comment_lines: 0\n")
	fmt.Printf("blank_lines: 0\n")
	fmt.Printf("hash_algorithm: %s\n", contentHashAlgorithm)
	fmt.Printf("fts5: %s\n", yesNo(fts))
	return nil
}

func cmdRebuild(args []string) error {
	return runRebuild(args)
}

func cmdUpdate(args []string) error {
	return runUpdate(args)
}

func runRebuild(args []string) error {
	flags := flag.NewFlagSet("rebuild", flag.ExitOnError)
	dbPath := flags.String("db", "", "database path")
	maxBytes := flags.Int64("max-bytes", 1_000_000, "skip files larger than this")
	var extraIgnored repeatedFlag
	flags.Var(&extraIgnored, "ignore-dir", "extra directory name to ignore")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return errors.New("usage: code-index rebuild [--db DB] [--max-bytes N] ROOT")
	}
	if _, err := exec.LookPath("sqlite3"); err != nil {
		return errors.New("sqlite3 command not found")
	}
	root, err := filepath.Abs(flags.Arg(0))
	if err != nil {
		return err
	}
	info, err := os.Stat(root)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("not a directory: %s", root)
	}
	db := *dbPath
	if db == "" {
		db = defaultDBPath(root)
	}
	if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
		return err
	}
	lock, err := acquireIndexLock(db, "rebuild", root)
	if err != nil {
		if isIndexLocked(err) {
			printLockSkipped(db)
			return nil
		}
		return err
	}
	defer lock.release()
	tmpDB, err := createTempDBPath(db)
	if err != nil {
		return err
	}
	installed := false
	defer func() {
		if !installed {
			_ = removeDBFiles(tmpDB)
		}
	}()
	fts := hasFTS5()
	writer, wait, err := sqliteWriter(tmpDB)
	if err != nil {
		return err
	}
	writerOK := false
	defer func() {
		if !writerOK {
			_ = writer.Close()
			_ = wait()
		}
	}()
	writeSchema(writer, fts)
	writeSQL(writer, "insert into meta(key, value) values(%s, %s);\n", quote("root"), quote(root))
	writeSQL(writer, "insert into meta(key, value) values(%s, %s);\n", quote("fts5"), quote(boolText(fts)))
	writeSQL(writer, "insert into meta(key, value) values(%s, %s);\n", quote("hash_algorithm"), quote(contentHashAlgorithm))
	ignored := cloneIgnored(extraIgnored)
	var fileCount, symbolCount, lineCount int
	var codeLineCount, commentLineCount, blankLineCount int
	nextFileID := 1
	nextSymbolID := 1
	err = walkGitTrackedFiles(root, ignored, *maxBytes, func(path string, info fs.FileInfo) error {
		index, err := scanFileIndex(root, path, info, *maxBytes)
		if err != nil {
			return nil
		}
		writeFileIndexInsertSQL(writer, index, fts, nextFileID, &nextSymbolID)
		fileCount++
		symbolCount += len(index.symbols)
		lineCount += len(index.lines)
		codeLineCount += index.metrics.codeLines
		commentLineCount += index.metrics.commentLines
		blankLineCount += index.metrics.blankLines
		nextFileID++
		return nil
	})
	if err != nil {
		return err
	}
	writeSQL(writer, "commit;\n")
	if err := writer.Close(); err != nil {
		return err
	}
	if err := wait(); err != nil {
		return err
	}
	writerOK = true
	if err := installBuiltDB(tmpDB, db); err != nil {
		return err
	}
	installed = true
	fmt.Printf("db: %s\n", db)
	fmt.Printf("root: %s\n", root)
	fmt.Printf("files: %d\n", fileCount)
	fmt.Printf("symbols: %d\n", symbolCount)
	fmt.Printf("lines: %d\n", lineCount)
	fmt.Printf("code_lines: %d\n", codeLineCount)
	fmt.Printf("comment_lines: %d\n", commentLineCount)
	fmt.Printf("blank_lines: %d\n", blankLineCount)
	fmt.Printf("hash_algorithm: %s\n", contentHashAlgorithm)
	fmt.Printf("fts5: %s\n", yesNo(fts))
	return nil
}

func runUpdate(args []string) error {
	flags := flag.NewFlagSet("update", flag.ExitOnError)
	dbPath := flags.String("db", "", "database path")
	maxBytes := flags.Int64("max-bytes", 1_000_000, "skip files larger than this")
	var extraIgnored repeatedFlag
	flags.Var(&extraIgnored, "ignore-dir", "extra directory name to ignore")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return errors.New("usage: code-index update [--db DB] [--max-bytes N] ROOT")
	}
	if _, err := exec.LookPath("sqlite3"); err != nil {
		return errors.New("sqlite3 command not found")
	}
	root, err := filepath.Abs(flags.Arg(0))
	if err != nil {
		return err
	}
	info, err := os.Stat(root)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("not a directory: %s", root)
	}
	db := *dbPath
	if db == "" {
		db = defaultDBPath(root)
	}
	if !fileExists(db) {
		if _, locked, err := readIndexLock(db); err != nil {
			return err
		} else if locked {
			printLockSkipped(db)
			return nil
		}
		return fmt.Errorf("index not found: %s; run init or rebuild first, or pass --db", db)
	}
	lock, err := acquireIndexLock(db, "update", root)
	if err != nil {
		if isIndexLocked(err) {
			printLockSkipped(db)
			return nil
		}
		return err
	}
	defer lock.release()
	existing, err := loadIndexedFileStates(db)
	if err != nil {
		return err
	}
	fts, err := dbHasFTS5Tables(db)
	if err != nil {
		return err
	}
	writer, wait, err := sqliteWriter(db)
	if err != nil {
		return err
	}
	writerOK := false
	defer func() {
		if !writerOK {
			_ = writer.Close()
			_ = wait()
		}
	}()
	writeSQL(writer, ".bail on\n")
	writeSQL(writer, ".timeout 5000\n")
	writeSQL(writer, "begin immediate;\n")
	writeSQL(writer, "insert or replace into meta(key, value) values(%s, %s);\n", quote("root"), quote(root))
	writeSQL(writer, "insert or replace into meta(key, value) values(%s, %s);\n", quote("hash_algorithm"), quote(contentHashAlgorithm))
	ignored := cloneIgnored(extraIgnored)
	seen := map[string]bool{}
	var added, updated, deleted, unchanged int
	var symbolCount, lineCount int
	var codeLineCount, commentLineCount, blankLineCount int
	err = walkGitTrackedFiles(root, ignored, *maxBytes, func(path string, info fs.FileInfo) error {
		index, err := scanFileIndex(root, path, info, *maxBytes)
		if err != nil {
			return nil
		}
		seen[index.path] = true
		state, existed := existing[index.path]
		if existed && state.contentHash == index.contentHash {
			unchanged++
			return nil
		}
		if existed {
			updated++
		} else {
			added++
		}
		writeFileIndexDeleteSQL(writer, index.path, fts)
		writeFileIndexInsertSQL(writer, index, fts, 0, nil)
		symbolCount += len(index.symbols)
		lineCount += len(index.lines)
		codeLineCount += index.metrics.codeLines
		commentLineCount += index.metrics.commentLines
		blankLineCount += index.metrics.blankLines
		return nil
	})
	if err != nil {
		return err
	}
	for rel := range existing {
		if seen[rel] {
			continue
		}
		writeFileIndexDeleteSQL(writer, rel, fts)
		deleted++
	}
	writeSQL(writer, "commit;\n")
	if err := writer.Close(); err != nil {
		return err
	}
	if err := wait(); err != nil {
		return err
	}
	writerOK = true
	fmt.Printf("db: %s\n", db)
	fmt.Printf("root: %s\n", root)
	fmt.Printf("added: %d\n", added)
	fmt.Printf("updated: %d\n", updated)
	fmt.Printf("deleted: %d\n", deleted)
	fmt.Printf("unchanged: %d\n", unchanged)
	fmt.Printf("symbols: %d\n", symbolCount)
	fmt.Printf("lines: %d\n", lineCount)
	fmt.Printf("code_lines: %d\n", codeLineCount)
	fmt.Printf("comment_lines: %d\n", commentLineCount)
	fmt.Printf("blank_lines: %d\n", blankLineCount)
	fmt.Printf("hash_algorithm: %s\n", contentHashAlgorithm)
	fmt.Printf("fts5: %s\n", yesNo(fts))
	return nil
}

func cmdDefs(args []string) error {
	fs := flag.NewFlagSet("defs", flag.ExitOnError)
	root := fs.String("root", "", "repository root for default database path")
	db := fs.String("db", "", "database path")
	kind := fs.String("kind", "", "symbol kind filter")
	language := fs.String("language", "", "language filter")
	limit := fs.Int("limit", 50, "maximum rows")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: code-index defs [--root ROOT|--db DB] [--kind KIND] [--language LANG] QUERY")
	}
	query := fs.Arg(0)
	where := "(name = " + quote(query) + " collate nocase or name like " + quote(query+"%") + " collate nocase or signature like " + quote("%"+query+"%") + " collate nocase or path like " + quote("%"+query+"%") + " collate nocase)"
	if *kind != "" {
		where += " and kind = " + quote(*kind)
	}
	if *language != "" {
		where += " and language = " + quote(*language)
	}
	sql := fmt.Sprintf(`select path, line, kind, name, language, signature
from symbols
where %s
order by
  case
    when name = %s collate nocase then 0
    when name like %s collate nocase then 1
    else 2
  end,
  path,
  line
limit %d;`, where, quote(query), quote(query+"%"), *limit)
	return runSQLitePrint(requiredDB(*db, *root), sql)
}

func cmdFiles(args []string) error {
	fs := flag.NewFlagSet("files", flag.ExitOnError)
	root := fs.String("root", "", "repository root for default database path")
	db := fs.String("db", "", "database path")
	language := fs.String("language", "", "language filter")
	limit := fs.Int("limit", 100, "maximum rows")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: code-index files [--root ROOT|--db DB] [--language LANG] QUERY")
	}
	query := fs.Arg(0)
	where := "path like " + quote("%"+query+"%") + " collate nocase"
	if *language != "" {
		where += " and language = " + quote(*language)
	}
	sql := fmt.Sprintf(`select path, language, size
from files
where %s
order by path
limit %d;`, where, *limit)
	return runSQLitePrint(requiredDB(*db, *root), sql)
}

func cmdSQL(args []string) error {
	fs := flag.NewFlagSet("sql", flag.ExitOnError)
	root := fs.String("root", "", "repository root for default database path")
	db := fs.String("db", "", "database path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	var query string
	if fs.NArg() == 0 {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}
		query = string(data)
	} else if fs.NArg() == 1 {
		query = fs.Arg(0)
	} else {
		return errors.New("usage: code-index sql [--root ROOT|--db DB] [SQL]")
	}
	if err := validateReadOnlySQL(query); err != nil {
		return err
	}
	return runSQLitePrint(requiredDB(*db, *root), query)
}

func cmdShow(args []string) error {
	fs := flag.NewFlagSet("show", flag.ExitOnError)
	root := fs.String("root", "", "repository root for default database path")
	db := fs.String("db", "", "database path")
	line := fs.Int("line", 0, "1-based line number")
	context := fs.Int("context", 3, "context lines")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 || *line <= 0 {
		return errors.New("usage: code-index show [--root ROOT|--db DB] --line N [--context N] PATH")
	}
	path := strings.TrimPrefix(filepath.ToSlash(fs.Arg(0)), "/")
	start := *line - *context
	if start < 1 {
		start = 1
	}
	end := *line + *context
	sql := fmt.Sprintf(`with target as (
  select id, path
  from files
  where path = %s or path like %s
  order by case when path = %s then 0 else 1 end, length(path)
  limit 1
)
select target.path as path, lines.line as line, lines.text as text
from lines join target on target.id = lines.file_id
where lines.line between %d and %d
order by lines.line;`, quote(path), quote("%"+path), quote(path), start, end)
	return runSQLitePrint(requiredDB(*db, *root), sql)
}

func cmdStats(args []string) error {
	fs := flag.NewFlagSet("stats", flag.ExitOnError)
	root := fs.String("root", "", "repository root for default database path")
	db := fs.String("db", "", "database path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	sql := `select 'root' as key, value from meta where key = 'root'
union all select 'files', cast(count(*) as text) from files
union all select 'symbols', cast(count(*) as text) from symbols
union all select 'lines', cast(count(*) as text) from lines
union all select 'code_lines', cast(coalesce(sum(code_lines), 0) as text) from file_metrics
union all select 'comment_lines', cast(coalesce(sum(comment_lines), 0) as text) from file_metrics
union all select 'blank_lines', cast(coalesce(sum(blank_lines), 0) as text) from file_metrics
union all select 'hash_algorithm', value from meta where key = 'hash_algorithm'
union all select 'fts5', value from meta where key = 'fts5';`
	return runSQLitePrint(requiredDB(*db, *root), sql)
}

func cmdMetrics(args []string) error {
	fs := flag.NewFlagSet("metrics", flag.ExitOnError)
	root := fs.String("root", "", "repository root for default database path")
	db := fs.String("db", "", "database path")
	language := fs.String("language", "", "language filter")
	limit := fs.Int("limit", 100, "maximum rows")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 1 {
		return errors.New("usage: code-index metrics [--root ROOT|--db DB] [--language LANG] [--limit N] [PATH_QUERY]")
	}
	where := "1 = 1"
	if *language != "" {
		where += " and language = " + quote(*language)
	}
	var sql string
	if fs.NArg() == 0 {
		sql = fmt.Sprintf(`select coalesce(language, '(unknown)') as language,
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
limit %d;`, where, *limit)
	} else {
		query := fs.Arg(0)
		where += " and path like " + quote("%"+query+"%") + " collate nocase"
		sql = fmt.Sprintf(`select path,
       language,
       line_count as lines,
       code_lines as code,
       comment_lines as comments,
       blank_lines as blank,
       symbol_count as symbols
from file_metrics
where %s
order by path
limit %d;`, where, *limit)
	}
	return runSQLitePrint(requiredDB(*db, *root), sql)
}

func cmdStatus(args []string) error {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	root := fs.String("root", "", "repository root for default database path")
	dbFlag := fs.String("db", "", "database path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: code-index status [--root ROOT|--db DB]")
	}
	db := requiredDB(*dbFlag, *root)
	dbExists := fileExists(db)
	lockInfo, locked, err := readIndexLock(db)
	if err != nil {
		return err
	}
	if !dbExists && !locked {
		return fmt.Errorf("index not found: %s; run init or rebuild first, or pass --db", db)
	}
	fmt.Println("key\tvalue")
	fmt.Printf("db\t%s\n", db)
	fmt.Printf("exists\t%s\n", yesNo(dbExists))
	fmt.Printf("locked\t%s\n", yesNo(locked))
	if locked {
		fmt.Printf("lock\t%s\n", indexLockPath(db))
		fmt.Printf("operation\t%s\n", lockInfo.operationName())
		fmt.Printf("pid\t%s\n", lockInfo.pidText())
		if lockInfo.startedAt != "" {
			fmt.Printf("started_at\t%s\n", lockInfo.startedAt)
		}
		if lockInfo.root != "" {
			fmt.Printf("root\t%s\n", lockInfo.root)
		}
	}
	return nil
}

type repeatedFlag []string

func (r *repeatedFlag) String() string {
	return strings.Join(*r, ",")
}

func (r *repeatedFlag) Set(value string) error {
	*r = append(*r, value)
	return nil
}

func defaultDBPath(root string) string {
	sum := sha256.Sum256([]byte(root))
	return filepath.Join(defaultCacheDir(), hex.EncodeToString(sum[:])[:16]+".sqlite")
}

func defaultCacheDir() string {
	if dir := os.Getenv("CODE_INDEX_CACHE_DIR"); dir != "" {
		return dir
	}
	if dir := os.Getenv("XDG_CACHE_HOME"); dir != "" {
		return filepath.Join(dir, "code-index")
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".cache", "code-index")
	}
	return filepath.Join(os.TempDir(), "code-index")
}

func indexLockPath(db string) string {
	return db + ".lock"
}

func acquireIndexLock(db, operation, root string) (*indexLock, error) {
	path := indexLockPath(db)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			info, ok, readErr := readIndexLock(db)
			if readErr != nil || !ok {
				return nil, fmt.Errorf("%w: index operation already in progress: %s", errIndexLocked, path)
			}
			return nil, fmt.Errorf("%w: index %s already in progress for %s (pid %s, lock: %s)", errIndexLocked, info.operationName(), db, info.pidText(), path)
		}
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			_ = file.Close()
			_ = os.Remove(path)
		}
	}()
	if _, err := fmt.Fprintf(
		file,
		"operation=%s\nroot=%s\npid=%d\nstarted_at=%s\n",
		operation,
		root,
		os.Getpid(),
		time.Now().UTC().Format(time.RFC3339),
	); err != nil {
		return nil, err
	}
	if err := file.Close(); err != nil {
		return nil, err
	}
	ok = true
	return &indexLock{path: path}, nil
}

func isIndexLocked(err error) bool {
	return errors.Is(err, errIndexLocked)
}

func (l *indexLock) release() {
	if l != nil {
		_ = os.Remove(l.path)
	}
}

func readIndexLock(db string) (indexLockInfo, bool, error) {
	data, err := os.ReadFile(indexLockPath(db))
	if errors.Is(err, os.ErrNotExist) {
		return indexLockInfo{}, false, nil
	}
	if err != nil {
		return indexLockInfo{}, false, err
	}
	return parseIndexLock(string(data)), true, nil
}

func parseIndexLock(text string) indexLockInfo {
	var info indexLockInfo
	for _, line := range strings.Split(text, "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch key {
		case "operation":
			info.operation = value
		case "root":
			info.root = value
		case "pid":
			info.pid = value
		case "started_at":
			info.startedAt = value
		}
	}
	return info
}

func (info indexLockInfo) operationName() string {
	if info.operation != "" {
		return info.operation
	}
	return "operation"
}

func (info indexLockInfo) pidText() string {
	if info.pid != "" {
		return info.pid
	}
	return "unknown"
}

func queryLockNotice(db string) (string, error) {
	info, locked, err := readIndexLock(db)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(db); err != nil {
		if locked && errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("index %s in progress: %s; no previous index is available yet", info.operationName(), db)
		}
		return "", fmt.Errorf("index not found: %s; run rebuild first, or pass --db", db)
	}
	if locked {
		return fmt.Sprintf("warning: index %s in progress; using previous index: %s\n", info.operationName(), db), nil
	}
	return "", nil
}

func printLockSkipped(db string) {
	info, locked, err := readIndexLock(db)
	if err != nil || !locked {
		fmt.Fprintf(os.Stderr, "index operation already in progress; skipped: %s\n", db)
		return
	}
	fmt.Fprintf(os.Stderr, "index %s already in progress; skipped: %s\n", info.operationName(), db)
}

func createTempDBPath(db string) (string, error) {
	file, err := os.CreateTemp(filepath.Dir(db), "."+filepath.Base(db)+".*.tmp")
	if err != nil {
		return "", err
	}
	name := file.Name()
	if err := file.Close(); err != nil {
		_ = removeDBFiles(name)
		return "", err
	}
	return name, nil
}

func installBuiltDB(tmpDB, db string) error {
	if err := removeDBSidecars(db); err != nil {
		return err
	}
	if err := os.Rename(tmpDB, db); err != nil {
		return err
	}
	_ = removeDBSidecars(tmpDB)
	return nil
}

func removeDBFiles(db string) error {
	if err := removeDBSidecars(db); err != nil {
		return err
	}
	if err := os.Remove(db); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func removeDBSidecars(db string) error {
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if err := os.Remove(db + suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func requiredDB(db, root string) string {
	if db != "" {
		return db
	}
	if root == "" {
		root = "."
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return defaultDBPath(root)
	}
	return defaultDBPath(abs)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func hasFTS5() bool {
	cmd := exec.Command("sqlite3", ":memory:", "create virtual table t using fts5(x);")
	return cmd.Run() == nil
}

func sqliteWriter(db string) (io.WriteCloser, func() error, error) {
	cmd := exec.Command("sqlite3", "-batch", db)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, err
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, nil, err
	}
	return stdin, cmd.Wait, nil
}

func sqliteQueryOutput(db, sql string) (string, error) {
	cmd := exec.Command("sqlite3", "-batch", "-noheader", "-separator", "\t", db, sql)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("sqlite3 query failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func loadIndexedFileStates(db string) (map[string]indexedFileState, error) {
	out, err := sqliteQueryOutput(db, "select path, content_hash, size, mtime from files order by path;")
	if err != nil {
		return nil, err
	}
	states := map[string]indexedFileState{}
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if line == "" {
			continue
		}
		cols := strings.Split(line, "\t")
		if len(cols) != 4 {
			return nil, fmt.Errorf("unexpected files row from sqlite3: %q", line)
		}
		size, err := strconv.ParseInt(cols[2], 10, 64)
		if err != nil {
			return nil, err
		}
		mtime, err := strconv.ParseInt(cols[3], 10, 64)
		if err != nil {
			return nil, err
		}
		states[cols[0]] = indexedFileState{
			contentHash: cols[1],
			size:        size,
			mtime:       mtime,
		}
	}
	return states, nil
}

func dbHasFTS5Tables(db string) (bool, error) {
	out, err := sqliteQueryOutput(db, "select count(*) from sqlite_master where type = 'table' and name in ('files_fts', 'symbols_fts');")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) == "2", nil
}

func writeSchema(w io.Writer, fts bool) {
	writeSQL(w, ".bail on\n")
	writeSQL(w, "begin;\n")
	writeSQL(w, `create table meta (
  key text primary key,
  value text not null
);
create table files (
  id integer primary key,
  path text not null unique,
  language text,
  extension text,
  size integer not null,
  mtime integer not null,
  content_hash text not null
);
create table symbols (
  id integer primary key,
  file_id integer not null references files(id) on delete cascade,
  path text not null,
  language text,
  kind text not null,
  name text not null,
  line integer not null,
  column integer not null,
  signature text not null,
  context text not null
);
create table lines (
  file_id integer not null references files(id) on delete cascade,
  line integer not null,
  text text not null,
  primary key (file_id, line)
);
create table file_metrics (
  file_id integer primary key references files(id) on delete cascade,
  path text not null unique,
  language text,
  line_count integer not null,
  blank_lines integer not null,
  comment_lines integer not null,
  code_lines integer not null,
  symbol_count integer not null
);
create index idx_files_path on files(path);
create index idx_files_language on files(language);
create index idx_symbols_name on symbols(name);
create index idx_symbols_path_line on symbols(path, line);
create index idx_symbols_language_kind on symbols(language, kind);
create index idx_file_metrics_path on file_metrics(path);
create index idx_file_metrics_language on file_metrics(language);
`)
	if fts {
		writeSQL(w, "create virtual table files_fts using fts5(path, language, content);\n")
		writeSQL(w, "create virtual table symbols_fts using fts5(name, kind, language, path, signature, context);\n")
	}
}

func runSQLitePrint(db, sql string) error {
	notice, err := queryLockNotice(db)
	if err != nil {
		return err
	}
	if notice != "" {
		fmt.Fprint(os.Stderr, notice)
	}
	cmd := exec.Command("sqlite3", "-batch", db)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	_, _ = io.WriteString(stdin, ".headers on\n.mode tabs\n")
	_, _ = io.WriteString(stdin, sql)
	if !strings.HasSuffix(strings.TrimSpace(sql), ";") {
		_, _ = io.WriteString(stdin, ";\n")
	}
	if err := stdin.Close(); err != nil {
		return err
	}
	return cmd.Wait()
}

func validateReadOnlySQL(query string) error {
	trimmed := strings.TrimSpace(query)
	lower := strings.ToLower(trimmed)
	if !(strings.HasPrefix(lower, "select") || strings.HasPrefix(lower, "with") || strings.HasPrefix(lower, "pragma")) {
		return errors.New("only read-only SELECT, WITH, or PRAGMA statements are allowed")
	}
	stripped := strings.TrimRight(trimmed, ";\n\t ")
	if strings.Contains(stripped, ";") {
		return errors.New("only one SQL statement is allowed")
	}
	blocked := regexp.MustCompile(`(?i)\b(insert|update|delete|drop|alter|create|replace|vacuum|attach|detach)\b`)
	if blocked.MatchString(query) {
		return errors.New("mutating SQL is not allowed")
	}
	return nil
}

func walkGitTrackedFiles(root string, ignored map[string]bool, maxBytes int64, fn func(path string, info fs.FileInfo) error) error {
	if err := requireGitWorkTree(root); err != nil {
		return err
	}
	cmd := exec.Command("git", "-C", root, "ls-files", "-z", "--", ".")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git ls-files failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	for _, rel := range strings.Split(string(out), "\x00") {
		if rel == "" {
			continue
		}
		rel = filepath.ToSlash(filepath.Clean(rel))
		if rel == "." || strings.HasPrefix(rel, "../") || filepath.IsAbs(rel) || ignoredPath(rel, ignored) {
			continue
		}
		if binaryExts[strings.ToLower(filepath.Ext(rel))] {
			continue
		}
		path := filepath.Join(root, filepath.FromSlash(rel))
		info, err := os.Lstat(path)
		if err != nil || info.IsDir() || !info.Mode().IsRegular() || info.Size() > maxBytes {
			continue
		}
		if err := fn(path, info); err != nil {
			return err
		}
	}
	return nil
}

func requireGitWorkTree(root string) error {
	cmd := exec.Command("git", "-C", root, "rev-parse", "--is-inside-work-tree")
	out, err := cmd.CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "true" {
		return fmt.Errorf("rebuild and update require a Git work tree: %s", root)
	}
	return nil
}

func ignoredPath(path string, ignored map[string]bool) bool {
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if ignored[part] {
			return true
		}
	}
	return false
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

func computeFileMetrics(language string, lines []string, symbolCount int) fileMetrics {
	metrics := fileMetrics{
		lineCount:   len(lines),
		symbolCount: symbolCount,
	}
	var blockEnd string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			metrics.blankLines++
			continue
		}
		if isCommentOnlyLine(language, trimmed, &blockEnd) {
			metrics.commentLines++
			continue
		}
		metrics.codeLines++
	}
	return metrics
}

func isCommentOnlyLine(language, trimmed string, blockEnd *string) bool {
	if *blockEnd != "" {
		if strings.Contains(trimmed, *blockEnd) {
			*blockEnd = ""
		}
		return true
	}

	if start, end, ok := blockCommentDelimiters(language); ok && strings.HasPrefix(trimmed, start) {
		if !strings.Contains(trimmed[len(start):], end) {
			*blockEnd = end
		}
		return true
	}

	for _, prefix := range lineCommentPrefixes(language) {
		if strings.HasPrefix(trimmed, prefix) {
			return true
		}
	}

	if start, end, ok := blockCommentDelimiters(language); ok {
		if index := strings.Index(trimmed, start); index >= 0 && !strings.Contains(trimmed[index+len(start):], end) {
			*blockEnd = end
		}
	}
	return false
}

func lineCommentPrefixes(language string) []string {
	switch language {
	case "clojure", "elisp", "scheme":
		return []string{";"}
	case "haskell", "lua":
		return []string{"--"}
	case "c", "cpp", "csharp", "go", "java", "javascript", "kotlin", "rust", "scala", "swift", "typescript":
		return []string{"//"}
	case "php":
		return []string{"//", "#"}
	case "dockerfile", "elixir", "make", "python", "ruby", "shell":
		return []string{"#"}
	default:
		return nil
	}
}

func blockCommentDelimiters(language string) (string, string, bool) {
	switch language {
	case "c", "cpp", "csharp", "css", "go", "java", "javascript", "kotlin", "php", "rust", "scala", "swift", "typescript":
		return "/*", "*/", true
	case "haskell":
		return "{-", "-}", true
	case "html", "vue":
		return "<!--", "-->", true
	case "lua":
		return "--[[", "]]", true
	case "ruby":
		return "=begin", "=end", true
	default:
		return "", "", false
	}
}

func extractSymbols(path, language string, lines []string) []symbol {
	patterns := symbolPatterns[language]
	if len(patterns) == 0 {
		return nil
	}
	var out []symbol
	seen := map[string]bool{}
	for i, line := range lines {
		for _, pattern := range patterns {
			match := pattern.re.FindStringSubmatchIndex(line)
			if match == nil || len(match) < 4 {
				continue
			}
			name := line[match[2]:match[3]]
			if skipSymbolNames[name] {
				continue
			}
			key := name + ":" + strconv.Itoa(i+1) + ":" + pattern.kind
			if seen[key] {
				continue
			}
			seen[key] = true
			start := i - 2
			if start < 0 {
				start = 0
			}
			end := i + 3
			if end > len(lines) {
				end = len(lines)
			}
			out = append(out, symbol{
				path:      path,
				language:  language,
				kind:      pattern.kind,
				name:      name,
				line:      i + 1,
				column:    match[2] + 1,
				signature: truncate(strings.TrimSpace(line), 500),
				context:   truncate(strings.Join(lines[start:end], "\n"), 2000),
			})
			break
		}
	}
	return out
}

func contentHash(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func quote(s string) string {
	s = strings.ReplaceAll(s, "\x00", "")
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func nullableQuote(s string) string {
	if s == "" {
		return "null"
	}
	return quote(s)
}

func writeSQL(w io.Writer, format string, args ...interface{}) {
	_, _ = fmt.Fprintf(w, format, args...)
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}

func boolText(v bool) string {
	if v {
		return "1"
	}
	return "0"
}

func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

func cloneIgnored(extra []string) map[string]bool {
	out := make(map[string]bool, len(ignoredDirs)+len(extra))
	for name, value := range ignoredDirs {
		out[name] = value
	}
	for _, name := range extra {
		if name != "" {
			out[name] = true
		}
	}
	keys := make([]string, 0, len(out))
	for key := range out {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return out
}
