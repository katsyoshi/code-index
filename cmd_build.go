package main

import (
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
)

func cmdInit(args []string) error {
	flags := flag.NewFlagSet("init", flag.ExitOnError)
	dbPath := flags.String("db", "", "database path")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return errors.New(commandUsage("init"))
	}
	if _, err := exec.LookPath("sqlite3"); err != nil {
		return errors.New("sqlite3 command not found")
	}
	root, err := resolveRoot(flags.Arg(0))
	if err != nil {
		return err
	}
	db := *dbPath
	if db == "" {
		db = defaultDBPath(root)
	}
	if err := ensureIndexDoesNotExist(db); err != nil {
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
	if err := ensureIndexDoesNotExist(db); err != nil {
		return err
	}
	fts := hasFTS5()
	if err := createEmptyIndexDB(db, root, "init", fts); err != nil {
		return err
	}
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

func ensureIndexDoesNotExist(db string) error {
	if _, err := os.Stat(db); err == nil {
		return fmt.Errorf("index already exists: %s; run rebuild to replace it", db)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func resolveRoot(path string) (string, error) {
	root, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(root)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("not a directory: %s", root)
	}
	return root, nil
}

func cmdRebuild(args []string) error {
	return runRebuild(args)
}

func cmdUpdate(args []string) error {
	return runUpdate(args)
}

type buildOptions struct {
	root         string
	db           string
	maxBytes     int64
	extraIgnored repeatedFlag
	adopt        bool
}

func parseBuildOptions(commandName string, args []string, allowAdopt bool) (buildOptions, error) {
	flags := flag.NewFlagSet(commandName, flag.ExitOnError)
	dbPath := flags.String("db", "", "database path")
	maxBytes := flags.Int64("max-bytes", 1_000_000, "skip files larger than this")
	adopt := false
	var extraIgnored repeatedFlag
	flags.Var(&extraIgnored, "ignore-dir", "extra directory name to ignore")
	if allowAdopt {
		flags.BoolVar(&adopt, "adopt", false, "adopt an index from another checkout or Git history")
	}
	if err := flags.Parse(args); err != nil {
		return buildOptions{}, err
	}
	if flags.NArg() != 1 {
		return buildOptions{}, errors.New(commandUsage(commandName))
	}
	if _, err := exec.LookPath("sqlite3"); err != nil {
		return buildOptions{}, errors.New("sqlite3 command not found")
	}
	root, err := resolveRoot(flags.Arg(0))
	if err != nil {
		return buildOptions{}, err
	}
	db := *dbPath
	if db == "" {
		db = defaultDBPath(root)
	}
	return buildOptions{
		root:         root,
		db:           db,
		maxBytes:     *maxBytes,
		extraIgnored: extraIgnored,
		adopt:        adopt,
	}, nil
}

func runRebuild(args []string) error {
	options, err := parseBuildOptions("rebuild", args, false)
	if err != nil {
		return err
	}
	root := options.root
	db := options.db
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
	ignored := cloneIgnored(options.extraIgnored)
	var fileCount, symbolCount, lineCount int
	var codeLineCount, commentLineCount, blankLineCount int
	nextFileID := 1
	nextSymbolID := 1
	err = walkGitTrackedFiles(root, ignored, options.maxBytes, func(path string, info fs.FileInfo) error {
		index, err := scanFileIndex(root, path, info, options.maxBytes)
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
	writeOperationMetaSQL(writer, root, "rebuild", fts)
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
	options, err := parseBuildOptions("update", args, true)
	if err != nil {
		return err
	}
	root := options.root
	db := options.db
	if !fileExists(db) {
		if _, locked, err := readActiveIndexLock(db); err != nil {
			return err
		} else if locked {
			printLockSkipped(db)
			return nil
		}
		return fmt.Errorf("index not found: %s; run init or rebuild first, or pass --db", db)
	}
	if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
		return err
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
	meta, err := loadMeta(db)
	if err != nil {
		return err
	}
	if err := validateUpdateCompatibility(meta, root, options.adopt); err != nil {
		return err
	}
	existing, err := loadIndexedFileStates(db)
	if err != nil {
		return err
	}
	candidates, candidateOnly := updateCandidatePaths(root, existing, meta)
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
	ignored := cloneIgnored(options.extraIgnored)
	seen := map[string]bool{}
	var added, updated, deleted int
	var symbolCount int
	err = walkGitTrackedFileSet(root, ignored, options.maxBytes, candidates, func(path string, info fs.FileInfo) error {
		index, err := scanFileIndex(root, path, info, options.maxBytes)
		if err != nil {
			return nil
		}
		seen[index.path] = true
		state, existed := existing[index.path]
		if existed && state.contentHash == index.contentHash {
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
		return nil
	})
	if err != nil {
		return err
	}
	for rel := range existing {
		if seen[rel] {
			continue
		}
		if candidateOnly && !candidates[rel] {
			continue
		}
		writeFileIndexDeleteSQL(writer, rel, fts)
		deleted++
	}
	writeOperationMetaSQL(writer, root, "update", fts)
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
	fmt.Printf("added_files: %d\n", added)
	fmt.Printf("updated_files: %d\n", updated)
	fmt.Printf("deleted_files: %d\n", deleted)
	fmt.Printf("symbols: %d\n", symbolCount)
	fmt.Printf("hash_algorithm: %s\n", contentHashAlgorithm)
	fmt.Printf("fts5: %s\n", yesNo(fts))
	return nil
}

func validateUpdateCompatibility(meta map[string]string, root string, adopt bool) error {
	compatibility, err := checkUpdateCompatibility(meta, root)
	if err != nil {
		return err
	}
	if compatibility.compatible || (adopt && compatibility.requiresAdopt) {
		return nil
	}
	switch compatibility.blocker {
	case "schema_version":
		return fmt.Errorf("index schema is incompatible: db schema_version=%s, tool schema_version=%s; run rebuild", meta["schema_version"], schemaVersion)
	case "file_source":
		return fmt.Errorf("index file source is incompatible: db file_source=%s, tool file_source=%s; run rebuild", meta["file_source"], fileSource)
	case "hash_algorithm":
		return fmt.Errorf("index hash algorithm is incompatible: db hash_algorithm=%s, tool hash_algorithm=%s; run rebuild", meta["hash_algorithm"], contentHashAlgorithm)
	case "different_checkout":
		return fmt.Errorf("index belongs to a different checkout: indexed_root=%s current_root=%s; run rebuild, or run update --adopt if this DB should belong to the current checkout", meta["root"], root)
	case "unknown_git_history":
		indexedHead := meta["vcs_head"]
		if indexedHead == "" {
			indexedHead = meta["vcs_revision"]
		}
		return fmt.Errorf("index belongs to an unknown Git history: indexed_head=%s; run rebuild, or run update --adopt if this DB should belong to the current checkout", indexedHead)
	default:
		return fmt.Errorf("index is incompatible with update: %s; run rebuild", compatibility.blocker)
	}
}

type updateCompatibility struct {
	compatible      bool
	requiresAdopt   bool
	rebuildRequired bool
	blocker         string
}

func checkUpdateCompatibility(meta map[string]string, root string) (updateCompatibility, error) {
	if got := meta["schema_version"]; got != "" && got != schemaVersion {
		return updateCompatibility{rebuildRequired: true, blocker: "schema_version"}, nil
	}
	if got := meta["file_source"]; got != "" && got != fileSource {
		return updateCompatibility{rebuildRequired: true, blocker: "file_source"}, nil
	}
	if got := meta["hash_algorithm"]; got != "" && got != contentHashAlgorithm {
		return updateCompatibility{rebuildRequired: true, blocker: "hash_algorithm"}, nil
	}
	indexedRoot := meta["root"]
	if indexedRoot != "" && indexedRoot != root {
		return updateCompatibility{requiresAdopt: true, blocker: "different_checkout"}, nil
	}
	indexedHead := meta["vcs_head"]
	if indexedHead == "" {
		indexedHead = meta["vcs_revision"]
	}
	if indexedHead != "" {
		exists, err := gitCommitExists(root, indexedHead)
		if err != nil {
			return updateCompatibility{}, err
		}
		if !exists {
			return updateCompatibility{requiresAdopt: true, blocker: "unknown_git_history"}, nil
		}
	}
	return updateCompatibility{compatible: true}, nil
}

func createEmptyIndexDB(db, root, operation string, fts bool) error {
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
	writeOperationMetaSQL(writer, root, operation, fts)
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
	return nil
}
