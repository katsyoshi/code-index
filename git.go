package main

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func currentVCSMeta(root string) []metaPair {
	if out, err := gitOutput(root, "rev-parse", "--is-inside-work-tree"); err != nil || out != "true" {
		return nil
	}
	pairs := []metaPair{{"vcs_kind", "git"}}
	if revision, err := gitOutput(root, "rev-parse", "--verify", "HEAD"); err == nil {
		pairs = append(pairs, metaPair{"vcs_revision", revision})
	}
	if ref, err := gitOutput(root, "symbolic-ref", "--quiet", "--short", "HEAD"); err == nil {
		pairs = append(pairs, metaPair{"vcs_ref", ref})
	}
	return pairs
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
