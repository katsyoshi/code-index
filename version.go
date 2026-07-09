package main

import "runtime/debug"

var buildCommit = "unknown"

type buildInfo struct {
	commit   string
	modified string
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
