package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
)

type componentStatus struct {
	Name      string `json:"name"`
	Status    string `json:"status"`
	UpdatedAt string `json:"updated_at"`
}

type statusJSONResult struct {
	DB                      string             `json:"db"`
	Exists                  bool               `json:"exists"`
	Locked                  bool               `json:"locked"`
	Root                    *string            `json:"root"`
	SchemaVersion           *int64             `json:"schema_version"`
	FileSource              *string            `json:"file_source"`
	HashAlgorithm           *string            `json:"hash_algorithm"`
	ConfigMaxBytes          *int64             `json:"config_max_bytes"`
	ConfigIgnoreDirs        *[]string          `json:"config_ignore_dirs"`
	ConfigEncodingFallbacks *[]string          `json:"config_encoding_fallbacks"`
	IndexedAt               *string            `json:"indexed_at"`
	UpdatedAt               *string            `json:"updated_at"`
	LastOperation           *string            `json:"last_operation"`
	VCSKind                 *string            `json:"vcs_kind"`
	VCSHead                 *string            `json:"vcs_head"`
	VCSBranch               *string            `json:"vcs_branch"`
	VCSDirty                *bool              `json:"vcs_dirty"`
	VCSDirtyHash            *string            `json:"vcs_dirty_hash"`
	VCSRevision             *string            `json:"vcs_revision"`
	VCSRef                  *string            `json:"vcs_ref"`
	FTS5                    *bool              `json:"fts5"`
	Components              *[]componentStatus `json:"components"`
	CurrentVCSKind          *string            `json:"current_vcs_kind"`
	CurrentVCSHead          *string            `json:"current_vcs_head"`
	CurrentVCSBranch        *string            `json:"current_vcs_branch"`
	CurrentVCSDirty         *bool              `json:"current_vcs_dirty"`
	UpdateCompatible        *bool              `json:"update_compatible"`
	UpdateRequiresAdopt     *bool              `json:"update_requires_adopt"`
	UpdateRebuildRequired   *bool              `json:"update_rebuild_required"`
	UpdateBlocker           *string            `json:"update_blocker"`
	IndexStale              *bool              `json:"index_stale"`
	Lock                    *string            `json:"lock"`
	LockOperation           *string            `json:"lock_operation"`
	LockPID                 *int               `json:"lock_pid"`
	LockStale               *bool              `json:"lock_stale"`
	LockStartedAt           *string            `json:"lock_started_at"`
	LockRoot                *string            `json:"lock_root"`
}

type currentStatusResult struct {
	kind            string
	head            string
	branch          string
	dirty           *bool
	compatible      *bool
	requiresAdopt   *bool
	rebuildRequired *bool
	blocker         string
	stale           *bool
	staleReported   bool
}

func cmdStatus(args []string) error {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	root := fs.String("root", "", "repository root for default database path")
	dbFlag := fs.String("db", "", "database path")
	formatFlag := fs.String("format", "text", "output format: text or json")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New(commandUsage("status"))
	}
	format, err := parseOutputFormat(*formatFlag)
	if err != nil {
		return err
	}
	db, resolvedRoot, err := resolveDB(*dbFlag, *root)
	if err != nil {
		return err
	}
	dbExists := fileExists(db)
	lockInfo, locked, err := readIndexLock(db)
	if err != nil {
		return err
	}
	if !dbExists && !locked {
		return fmt.Errorf("index not found: %s; run init or rebuild first, or pass --db", db)
	}
	if format == outputFormatJSON {
		return writeStatusJSON(db, resolvedRoot, dbExists, lockInfo, locked)
	}
	fmt.Println("key\tvalue")
	fmt.Printf("db\t%s\n", db)
	fmt.Printf("exists\t%s\n", yesNo(dbExists))
	fmt.Printf("locked\t%s\n", yesNo(locked))
	if dbExists {
		meta, err := loadMeta(db)
		if err != nil {
			return err
		}
		printMetaStatus(meta)
		components, known, err := loadComponents(db)
		if err != nil {
			return err
		}
		if known {
			encoded, err := json.Marshal(components)
			if err != nil {
				return err
			}
			fmt.Printf("components\t%s\n", encoded)
		}
		if err := printCurrentStatus(resolvedRoot, meta); err != nil {
			return err
		}
	}
	if locked {
		fmt.Printf("lock\t%s\n", indexLockPath(db))
		fmt.Printf("lock_operation\t%s\n", lockInfo.operationName())
		fmt.Printf("lock_pid\t%s\n", lockInfo.pidText())
		fmt.Printf("lock_stale\t%s\n", yesNo(isStaleIndexLock(lockInfo)))
		if lockInfo.startedAt != "" {
			fmt.Printf("lock_started_at\t%s\n", lockInfo.startedAt)
		}
		if lockInfo.root != "" {
			fmt.Printf("lock_root\t%s\n", lockInfo.root)
		}
	}
	return nil
}

func printMetaStatus(meta map[string]string) {
	for _, key := range []string{
		"root",
		"schema_version",
		"file_source",
		"hash_algorithm",
		"config_max_bytes",
		"config_ignore_dirs",
		"config_encoding_fallbacks",
		"indexed_at",
		"updated_at",
		"last_operation",
		"vcs_kind",
		"vcs_head",
		"vcs_branch",
		"vcs_dirty",
		"vcs_dirty_hash",
		"vcs_revision",
		"vcs_ref",
		"fts5",
	} {
		if value := meta[key]; value != "" {
			fmt.Printf("%s\t%s\n", key, value)
		}
	}
}

func printCurrentStatus(root string, meta map[string]string) error {
	result, err := collectCurrentStatus(root, meta)
	if err != nil {
		return err
	}
	if result == nil {
		return nil
	}
	fmt.Printf("current_vcs_kind\t%s\n", result.kind)
	if result.head != "" {
		fmt.Printf("current_vcs_head\t%s\n", result.head)
	}
	if result.branch != "" {
		fmt.Printf("current_vcs_branch\t%s\n", result.branch)
	}
	if result.dirty != nil {
		fmt.Printf("current_vcs_dirty\t%s\n", yesNo(*result.dirty))
	}
	fmt.Printf("update_compatible\t%s\n", yesNo(*result.compatible))
	fmt.Printf("update_requires_adopt\t%s\n", yesNo(*result.requiresAdopt))
	fmt.Printf("update_rebuild_required\t%s\n", yesNo(*result.rebuildRequired))
	if result.blocker != "" {
		fmt.Printf("update_blocker\t%s\n", result.blocker)
	}
	if !result.staleReported {
		return nil
	}
	if result.stale == nil {
		fmt.Println("index_stale\tunknown")
	} else {
		fmt.Printf("index_stale\t%s\n", yesNo(*result.stale))
	}
	return nil
}

func collectCurrentStatus(root string, meta map[string]string) (*currentStatusResult, error) {
	if root == "" {
		root = meta["root"]
	}
	if root == "" {
		return nil, nil
	}
	status, ok := currentVCSStatus(root)
	if !ok {
		return nil, nil
	}
	result := &currentStatusResult{kind: status.kind, head: status.revision, branch: status.ref}
	if status.dirty != "" {
		result.dirty = boolPointer(status.dirty == boolText(true))
	}
	config, err := resolveConfig(root)
	if err != nil {
		return nil, err
	}
	compatibility, err := checkUpdateCompatibility(meta, root, config.build, hasFTS5())
	if err != nil {
		return nil, err
	}
	result.compatible = boolPointer(compatibility.compatible)
	result.requiresAdopt = boolPointer(compatibility.requiresAdopt)
	result.rebuildRequired = boolPointer(compatibility.rebuildRequired)
	result.blocker = compatibility.blocker
	if status.revision == "" && status.dirty == "" {
		return result, nil
	}
	indexedHead := meta["vcs_head"]
	if indexedHead == "" {
		indexedHead = meta["vcs_revision"]
	}
	if indexedHead == "" {
		result.staleReported = true
		return result, nil
	}
	stale, known, err := currentIndexStale(root, meta, status, indexedHead)
	if err != nil {
		return nil, err
	}
	if known {
		result.stale = boolPointer(stale)
	}
	result.staleReported = true
	return result, nil
}

func writeStatusJSON(db, root string, dbExists bool, lockInfo indexLockInfo, locked bool) error {
	result := statusJSONResult{DB: db, Exists: dbExists, Locked: locked}
	if dbExists {
		meta, err := loadMeta(db)
		if err != nil {
			return err
		}
		result.Root = stringPointer(meta["root"])
		result.SchemaVersion = int64Pointer(meta["schema_version"])
		result.FileSource = stringPointer(meta["file_source"])
		result.HashAlgorithm = stringPointer(meta["hash_algorithm"])
		result.ConfigMaxBytes = int64Pointer(meta["config_max_bytes"])
		result.ConfigIgnoreDirs = stringSlicePointer(meta["config_ignore_dirs"])
		result.ConfigEncodingFallbacks = stringSlicePointer(meta["config_encoding_fallbacks"])
		result.IndexedAt = stringPointer(meta["indexed_at"])
		result.UpdatedAt = stringPointer(meta["updated_at"])
		result.LastOperation = stringPointer(meta["last_operation"])
		result.VCSKind = stringPointer(meta["vcs_kind"])
		result.VCSHead = stringPointer(meta["vcs_head"])
		result.VCSBranch = stringPointer(meta["vcs_branch"])
		result.VCSDirty = storedBoolPointer(meta["vcs_dirty"])
		result.VCSDirtyHash = stringPointer(meta["vcs_dirty_hash"])
		result.VCSRevision = stringPointer(meta["vcs_revision"])
		result.VCSRef = stringPointer(meta["vcs_ref"])
		result.FTS5 = storedBoolPointer(meta["fts5"])
		components, known, err := loadComponents(db)
		if err != nil {
			return err
		}
		if known {
			result.Components = &components
		}
		current, err := collectCurrentStatus(root, meta)
		if err != nil {
			return err
		}
		if current != nil {
			result.CurrentVCSKind = stringPointer(current.kind)
			result.CurrentVCSHead = stringPointer(current.head)
			result.CurrentVCSBranch = stringPointer(current.branch)
			result.CurrentVCSDirty = current.dirty
			result.UpdateCompatible = current.compatible
			result.UpdateRequiresAdopt = current.requiresAdopt
			result.UpdateRebuildRequired = current.rebuildRequired
			result.UpdateBlocker = stringPointer(current.blocker)
			result.IndexStale = current.stale
		}
	}
	if locked {
		result.Lock = stringPointer(indexLockPath(db))
		result.LockOperation = stringPointer(lockInfo.operationName())
		result.LockPID = intPointer(lockInfo.pid)
		result.LockStale = boolPointer(isStaleIndexLock(lockInfo))
		result.LockStartedAt = stringPointer(lockInfo.startedAt)
		result.LockRoot = stringPointer(lockInfo.root)
	}
	return writeJSON(os.Stdout, result)
}

func loadComponents(db string) ([]componentStatus, bool, error) {
	exists, err := sqliteQueryOutput(db, "select count(*) from sqlite_master where type = 'table' and name = 'components';")
	if err != nil {
		return nil, false, err
	}
	if strings.TrimSpace(exists) != "1" {
		return nil, false, nil
	}
	out, err := sqliteQueryOutput(db, mustEmbeddedSQL("components_select.sql"))
	if err != nil {
		return nil, false, err
	}
	components := make([]componentStatus, 0, 5)
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if line == "" {
			continue
		}
		columns := strings.Split(line, "\t")
		if len(columns) != 3 {
			return nil, false, fmt.Errorf("unexpected component row from sqlite3: %q", line)
		}
		components = append(components, componentStatus{Name: columns[0], Status: columns[1], UpdatedAt: columns[2]})
	}
	return components, true, nil
}

func stringPointer(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func intPointer(value string) *int {
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return nil
	}
	return &parsed
}

func int64Pointer(value string) *int64 {
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return nil
	}
	return &parsed
}

func boolPointer(value bool) *bool {
	return &value
}

func storedBoolPointer(value string) *bool {
	switch value {
	case "1":
		return boolPointer(true)
	case "0":
		return boolPointer(false)
	default:
		return nil
	}
}

func stringSlicePointer(value string) *[]string {
	if value == "" {
		return nil
	}
	var parsed []string
	if err := json.Unmarshal([]byte(value), &parsed); err != nil {
		return nil
	}
	return &parsed
}

func currentIndexStale(root string, meta map[string]string, status vcsStatus, indexedHead string) (bool, bool, error) {
	if status.revision != indexedHead {
		return true, true, nil
	}
	if status.dirty != boolText(true) {
		return false, true, nil
	}
	if meta["vcs_dirty"] != boolText(true) {
		return true, true, nil
	}
	currentHash, ok, err := currentDirtyHash(root)
	if err != nil {
		return false, false, err
	}
	if !ok || meta["vcs_dirty_hash"] == "" {
		return false, false, nil
	}
	return currentHash != meta["vcs_dirty_hash"], true, nil
}
