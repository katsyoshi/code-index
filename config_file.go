package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

const projectConfigName = ".code-index.toml"

type configScope string

const (
	configScopeDefault configScope = "default"
	configScopeProject configScope = "project"
	configScopeUser    configScope = "user"
	configScopePC      configScope = "pc"
)

type fileConfig struct {
	DB         *string   `toml:"db"`
	MaxBytes   *int64    `toml:"max_bytes"`
	IgnoreDirs *[]string `toml:"ignore_dirs"`
}

type resolvedConfig struct {
	build buildConfig
	db    string
	path  string
	scope configScope
}

func resolveRootOrCurrent(path string) (string, error) {
	if path != "" {
		return resolveRoot(path)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	if root, err := gitOutput(cwd, "rev-parse", "--show-toplevel"); err == nil && root != "" {
		return resolveRoot(root)
	}
	return resolveRoot(cwd)
}

func resolveConfig(root string) (resolvedConfig, error) {
	candidates := []struct {
		path  string
		scope configScope
	}{
		{filepath.Join(root, projectConfigName), configScopeProject},
	}
	if path := userConfigPath(); path != "" {
		candidates = append(candidates, struct {
			path  string
			scope configScope
		}{path, configScopeUser})
	}
	candidates = append(candidates, struct {
		path  string
		scope configScope
	}{filepath.Join(string(filepath.Separator), "etc", "code-index", "config.toml"), configScopePC})

	result := resolvedConfig{build: defaultBuildConfig(), scope: configScopeDefault}
	for _, candidate := range candidates {
		info, err := os.Stat(candidate.path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return resolvedConfig{}, fmt.Errorf("inspect config %s: %w", candidate.path, err)
		}
		if info.IsDir() {
			return resolvedConfig{}, fmt.Errorf("config is a directory: %s", candidate.path)
		}
		config, err := parseConfigFile(candidate.path)
		if err != nil {
			return resolvedConfig{}, err
		}
		result.path = candidate.path
		result.scope = candidate.scope
		if config.MaxBytes != nil {
			if *config.MaxBytes <= 0 {
				return resolvedConfig{}, fmt.Errorf("invalid max_bytes in %s: must be a positive integer", candidate.path)
			}
			result.build.maxBytes = *config.MaxBytes
		}
		if config.IgnoreDirs != nil {
			result.build.ignoreDirs = ignoredDirNames(*config.IgnoreDirs)
		}
		if config.DB != nil {
			if candidate.scope != configScopeProject {
				return resolvedConfig{}, fmt.Errorf("db is only allowed in project config: %s", candidate.path)
			}
			db, err := resolveProjectDB(root, *config.DB)
			if err != nil {
				return resolvedConfig{}, fmt.Errorf("invalid db in %s: %w", candidate.path, err)
			}
			result.db = db
		}
		break
	}
	return result, nil
}

func userConfigPath() string {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "code-index", "config.toml")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".config", "code-index", "config.toml")
}

func resolveProjectDB(root, value string) (string, error) {
	if value == "" {
		return "", errors.New("db must not be empty")
	}
	if filepath.IsAbs(value) {
		return "", errors.New("db must be relative to the repository root")
	}
	path := filepath.Clean(filepath.Join(root, value))
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("db must stay within the repository root")
	}
	return path, nil
}

func parseConfigFile(path string) (fileConfig, error) {
	var config fileConfig
	metadata, err := toml.DecodeFile(path, &config)
	if err != nil {
		return fileConfig{}, fmt.Errorf("parse config %s: %w", path, err)
	}
	if undecoded := metadata.Undecoded(); len(undecoded) != 0 {
		keys := make([]string, 0, len(undecoded))
		for _, key := range undecoded {
			keys = append(keys, key.String())
		}
		return fileConfig{}, fmt.Errorf("config %s: unknown key %s", path, strings.Join(keys, ", "))
	}
	return config, nil
}

func resolveDB(db, rootOption string) (string, string, error) {
	if db != "" {
		return db, rootOption, nil
	}
	root, err := resolveRootOrCurrent(rootOption)
	if err != nil {
		return "", "", err
	}
	config, err := resolveConfig(root)
	if err != nil {
		return "", "", err
	}
	if config.db != "" {
		return config.db, root, nil
	}
	return defaultDBPath(root), root, nil
}
