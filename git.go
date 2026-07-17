package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

type vcsStatus struct {
	kind     string
	revision string
	ref      string
	dirty    string
}

func currentVCSMeta(root string) []metaPair {
	status, ok := currentVCSStatus(root)
	if !ok {
		return nil
	}
	pairs := []metaPair{{"vcs_kind", status.kind}}
	if status.revision != "" {
		pairs = append(pairs,
			metaPair{"vcs_revision", status.revision},
			metaPair{"vcs_head", status.revision},
		)
	}
	if status.ref != "" {
		pairs = append(pairs,
			metaPair{"vcs_ref", status.ref},
			metaPair{"vcs_branch", status.ref},
		)
	}
	if status.dirty != "" {
		pairs = append(pairs, metaPair{"vcs_dirty", status.dirty})
		if dirtyHash, ok, err := currentDirtyHash(root); err == nil && ok && dirtyHash != "" {
			pairs = append(pairs, metaPair{"vcs_dirty_hash", dirtyHash})
		}
	}
	return pairs
}

func currentVCSStatus(root string) (vcsStatus, bool) {
	if out, err := gitOutput(root, "rev-parse", "--is-inside-work-tree"); err != nil || out != "true" {
		return vcsStatus{}, false
	}
	status := vcsStatus{kind: "git"}
	if revision, err := gitOutput(root, "rev-parse", "--verify", "HEAD"); err == nil {
		status.revision = revision
	}
	if ref, err := gitOutput(root, "symbolic-ref", "--quiet", "--short", "HEAD"); err == nil {
		status.ref = ref
	}
	if out, err := gitOutput(root, "status", "--porcelain", "--untracked-files=no"); err == nil {
		status.dirty = boolText(out != "")
	}
	return status, true
}

func gitOutput(root string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s failed: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

func walkGitTrackedFiles(root string, ignored map[string]bool, maxBytes int64, fn func(path string, info fs.FileInfo) error) error {
	return walkGitTrackedFileSet(root, ignored, maxBytes, nil, fn)
}

func walkGitTrackedFileSet(root string, ignored map[string]bool, maxBytes int64, candidates map[string]bool, fn func(path string, info fs.FileInfo) error) error {
	if err := requireGitWorkTree(root); err != nil {
		return err
	}
	args := []string{"-C", root, "ls-files", "-z", "--"}
	if candidates == nil {
		args = append(args, ".")
	} else {
		paths := make([]string, 0, len(candidates))
		for path := range candidates {
			paths = append(paths, path)
		}
		if len(paths) == 0 {
			return nil
		}
		sort.Strings(paths)
		args = append(args, paths...)
	}
	cmd := exec.Command("git", args...)
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

func updateCandidatePaths(root string, existing map[string]indexedFileState, meta map[string]string) (map[string]bool, bool) {
	if len(existing) == 0 || meta["vcs_dirty"] != boolText(false) || meta["vcs_revision"] == "" {
		return nil, false
	}
	current, ok := currentVCSStatus(root)
	if !ok || current.revision == "" {
		return nil, false
	}
	candidates := map[string]bool{}
	if current.revision != meta["vcs_revision"] {
		paths, err := gitDiffNameOnly(root, meta["vcs_revision"], current.revision)
		if err != nil {
			return nil, false
		}
		for _, path := range paths {
			candidates[path] = true
		}
	}
	dirty, err := dirtyCandidatePaths(root)
	if err != nil {
		return nil, false
	}
	for path := range dirty {
		candidates[path] = true
	}
	return candidates, true
}

func dirtyCandidatePaths(root string) (map[string]bool, error) {
	candidates := map[string]bool{}
	for _, args := range [][]string{
		{"diff", "--name-only", "-z", "--no-renames", "--", "."},
		{"diff", "--name-only", "-z", "--no-renames", "--cached", "--", "."},
	} {
		paths, err := gitNameOnly(root, args...)
		if err != nil {
			return nil, err
		}
		for _, path := range paths {
			candidates[path] = true
		}
	}
	return candidates, nil
}

func currentDirtyHash(root string) (string, bool, error) {
	candidates, err := dirtyCandidatePaths(root)
	if err != nil {
		return "", false, err
	}
	parts := []string{}
	seen := map[string]bool{}
	err = walkGitTrackedFileSet(root, cloneIgnored(nil), 1_000_000, candidates, func(path string, info fs.FileInfo) error {
		index, err := scanFileIndex(root, path, info, 1_000_000)
		if err != nil {
			return nil
		}
		seen[index.path] = true
		parts = append(parts, index.path+"\t"+index.contentHash)
		return nil
	})
	if err != nil {
		return "", false, err
	}
	for rel := range candidates {
		if !seen[rel] {
			parts = append(parts, rel+"\t-")
		}
	}
	sort.Strings(parts)
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:]), true, nil
}

func gitDiffNameOnly(root, oldRevision, newRevision string) ([]string, error) {
	return gitNameOnly(root, "diff", "--name-only", "-z", "--no-renames", oldRevision, newRevision, "--", ".")
}

func gitCommitExists(root, revision string) (bool, error) {
	if revision == "" {
		return false, nil
	}
	if err := requireGitWorkTree(root); err != nil {
		return false, err
	}
	cmd := exec.Command("git", "-C", root, "cat-file", "-e", revision+"^{commit}")
	if err := cmd.Run(); err != nil {
		return false, nil
	}
	return true, nil
}

func gitNameOnly(root string, args ...string) ([]string, error) {
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git %s failed: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	paths := []string{}
	for _, rel := range strings.Split(string(out), "\x00") {
		if rel == "" {
			continue
		}
		rel = filepath.ToSlash(filepath.Clean(rel))
		if rel == "." || strings.HasPrefix(rel, "../") || filepath.IsAbs(rel) {
			continue
		}
		paths = append(paths, rel)
	}
	return paths, nil
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
