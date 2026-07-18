package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const schemaVersion = "1"
const contentHashAlgorithm = "sha256"
const fileSource = "git-tracked"

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
	cmd := exec.Command("sqlite3", ":memory:", mustEmbeddedSQL("fts5_probe.sql"))
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

func sqliteJSONQuery(db, sql string, destination any) error {
	notice, err := queryLockNotice(db)
	if err != nil {
		return err
	}
	if notice != "" {
		fmt.Fprint(os.Stderr, notice)
	}
	cmd := exec.Command("sqlite3", "-batch", "-json", db, sql)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("sqlite3 JSON query failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	if len(bytes.TrimSpace(out)) == 0 {
		out = []byte("[]")
	}
	if err := json.Unmarshal(out, destination); err != nil {
		return fmt.Errorf("invalid JSON from sqlite3: %w", err)
	}
	return nil
}

type metaPair struct {
	key   string
	value string
}

type buildConfig struct {
	maxBytes   int64
	ignoreDirs []string
}

func defaultBuildConfig() buildConfig {
	return buildConfig{
		maxBytes:   defaultMaxBytes,
		ignoreDirs: ignoredDirNames(nil),
	}
}

func writeOperationMetaSQL(w io.Writer, root, operation string, fts bool, config buildConfig) {
	now := time.Now().UTC().Format(time.RFC3339)
	pairs := []metaPair{
		{"schema_version", schemaVersion},
		{"root", root},
		{"file_source", fileSource},
		{"hash_algorithm", contentHashAlgorithm},
		{"config_max_bytes", int64Text(config.maxBytes)},
		{"config_ignore_dirs", stringListText(config.ignoreDirs)},
		{"fts5", boolText(fts)},
		{"indexed_at", now},
		{"updated_at", now},
		{"last_operation", operation},
	}
	pairs = append(pairs, currentVCSMeta(root)...)
	for _, pair := range pairs {
		if pair.value == "" {
			continue
		}
		writeSQL(w, "%s\n", formatEmbeddedSQL("meta_upsert.sql", quote(pair.key), quote(pair.value)))
	}
}

func loadMeta(db string) (map[string]string, error) {
	out, err := sqliteQueryOutput(db, mustEmbeddedSQL("meta_select.sql"))
	if err != nil {
		return nil, err
	}
	meta := map[string]string{}
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if line == "" {
			continue
		}
		key, value, ok := strings.Cut(line, "\t")
		if !ok {
			return nil, fmt.Errorf("unexpected meta row from sqlite3: %q", line)
		}
		meta[key] = value
	}
	return meta, nil
}

func loadIndexedFileStates(db string) (map[string]indexedFileState, error) {
	out, err := sqliteQueryOutput(db, mustEmbeddedSQL("file_states.sql"))
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
	out, err := sqliteQueryOutput(db, mustEmbeddedSQL("fts5_table_count.sql"))
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) == "2", nil
}

func writeSchema(w io.Writer, fts bool) {
	writeSQL(w, ".bail on\n")
	writeSQL(w, "begin;\n")
	writeSQL(w, "%s\n", mustEmbeddedSQL("schema.sql"))
	if fts {
		writeSQL(w, "%s\n", mustEmbeddedSQL("schema_fts.sql"))
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
