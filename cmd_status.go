package main

import (
	"errors"
	"flag"
	"fmt"
)

func cmdStatus(args []string) error {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	root := fs.String("root", "", "repository root for default database path")
	dbFlag := fs.String("db", "", "database path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New(commandUsage("status"))
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
	if dbExists {
		meta, err := loadMeta(db)
		if err != nil {
			return err
		}
		printMetaStatus(meta)
		if err := printCurrentStatus(*root, meta); err != nil {
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
	if root == "" {
		root = meta["root"]
	}
	if root == "" {
		return nil
	}
	status, ok := currentVCSStatus(root)
	if !ok {
		return nil
	}
	fmt.Printf("current_vcs_kind\t%s\n", status.kind)
	if status.revision != "" {
		fmt.Printf("current_vcs_head\t%s\n", status.revision)
	}
	if status.ref != "" {
		fmt.Printf("current_vcs_branch\t%s\n", status.ref)
	}
	if status.dirty != "" {
		fmt.Printf("current_vcs_dirty\t%s\n", yesNo(status.dirty == boolText(true)))
	}
	compatibility, err := checkUpdateCompatibility(meta, root)
	if err != nil {
		return err
	}
	fmt.Printf("update_compatible\t%s\n", yesNo(compatibility.compatible))
	fmt.Printf("update_requires_adopt\t%s\n", yesNo(compatibility.requiresAdopt))
	fmt.Printf("update_rebuild_required\t%s\n", yesNo(compatibility.rebuildRequired))
	if compatibility.blocker != "" {
		fmt.Printf("update_blocker\t%s\n", compatibility.blocker)
	}
	if status.revision == "" && status.dirty == "" {
		return nil
	}
	indexedHead := meta["vcs_head"]
	if indexedHead == "" {
		indexedHead = meta["vcs_revision"]
	}
	if indexedHead == "" {
		fmt.Println("index_stale\tunknown")
		return nil
	}
	stale, known, err := currentIndexStale(root, meta, status, indexedHead)
	if err != nil {
		return err
	}
	if !known {
		fmt.Println("index_stale\tunknown")
		return nil
	}
	fmt.Printf("index_stale\t%s\n", yesNo(stale))
	return nil
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
