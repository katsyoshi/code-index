package main

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"
)

const defaultMaxBytes int64 = 1_000_000

type repeatedFlag []string

func (r *repeatedFlag) String() string {
	return strings.Join(*r, ",")
}

func (r *repeatedFlag) Set(value string) error {
	*r = append(*r, value)
	return nil
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

func int64Text(v int64) string {
	return strconv.FormatInt(v, 10)
}

func stringListText(values []string) string {
	data, err := json.Marshal(values)
	if err != nil {
		return "[]"
	}
	return string(data)
}

func ignoredDirNames(extra []string) []string {
	values := make(map[string]bool, len(ignoredDirs)+len(extra))
	for name, value := range ignoredDirs {
		if value {
			values[name] = true
		}
	}
	for _, name := range extra {
		if name != "" {
			values[name] = true
		}
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func cloneIgnored(extra []string) map[string]bool {
	out := make(map[string]bool, len(ignoredDirs)+len(extra))
	for _, name := range ignoredDirNames(extra) {
		out[name] = true
	}
	return out
}
