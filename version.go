package main

import (
	"runtime/debug"
	"strconv"
)

var buildCommit = "unknown"

type buildInfo struct {
	commit   string
	modified string
}

type versionJSONResult struct {
	Commit        *string `json:"commit"`
	Modified      *bool   `json:"modified"`
	SchemaVersion int64   `json:"schema_version"`
	FileSource    string  `json:"file_source"`
}

func currentBuildInfo() buildInfo {
	info := buildInfo{commit: buildCommit}
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range bi.Settings {
			switch setting.Key {
			case "vcs.revision":
				if setting.Value != "" {
					info.commit = setting.Value
				}
			case "vcs.modified":
				if setting.Value != "" {
					info.modified = setting.Value
				}
			}
		}
	}
	return info
}

func versionCommitPointer(value string) *string {
	if value == "" || value == "unknown" {
		return nil
	}
	return &value
}

func versionModifiedPointer(value string) *bool {
	modified, err := strconv.ParseBool(value)
	if err != nil {
		return nil
	}
	return &modified
}
