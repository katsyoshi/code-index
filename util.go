package main

import (
	"sort"
	"strings"
)

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

func cloneIgnored(extra []string) map[string]bool {
	out := make(map[string]bool, len(ignoredDirs)+len(extra))
	for name, value := range ignoredDirs {
		out[name] = value
	}
	for _, name := range extra {
		if name != "" {
			out[name] = true
		}
	}
	keys := make([]string, 0, len(out))
	for key := range out {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return out
}
