package main

import (
	"regexp"
	"strconv"
	"strings"
)

type symbolSpec struct {
	re   *regexp.Regexp
	kind string
}

type symbol struct {
	path      string
	language  string
	kind      string
	name      string
	line      int
	column    int
	signature string
	context   string
}

var symbolPatterns = map[string][]symbolSpec{
	"python": {
		spec(`^\s*(?:async\s+)?def\s+([A-Za-z_]\w*)\s*\(`, "function"),
		spec(`^\s*class\s+([A-Za-z_]\w*)\b`, "class"),
	},
	"ruby": {
		spec(`^\s*def\s+((?:self\.)?[A-Za-z_]\w*[!?=]?)\b`, "method"),
		spec(`^\s*class\s+([A-Za-z_:]\w*(?:::\w+)*)\b`, "class"),
		spec(`^\s*module\s+([A-Za-z_:]\w*(?:::\w+)*)\b`, "module"),
	},
	"javascript": {
		spec(`^\s*(?:export\s+)?(?:async\s+)?function\s+([$A-Za-z_][\w$]*)\s*\(`, "function"),
		spec(`^\s*(?:export\s+)?(?:const|let|var)\s+([$A-Za-z_][\w$]*)\s*=\s*(?:async\s*)?(?:\([^)]*\)|[$A-Za-z_][\w$]*)\s*=>`, "function"),
		spec(`^\s*(?:export\s+)?class\s+([$A-Za-z_][\w$]*)\b`, "class"),
		spec(`^\s*(?:static\s+|async\s+|get\s+|set\s+)*([$A-Za-z_][\w$]*)\s*\([^)]*\)\s*\{?\s*$`, "method"),
	},
	"typescript": {
		spec(`^\s*(?:export\s+)?(?:async\s+)?function\s+([$A-Za-z_][\w$]*)\s*\(`, "function"),
		spec(`^\s*(?:export\s+)?(?:const|let|var)\s+([$A-Za-z_][\w$]*)\s*[:=].*=>`, "function"),
		spec(`^\s*(?:export\s+)?(?:abstract\s+)?class\s+([$A-Za-z_][\w$]*)\b`, "class"),
		spec(`^\s*(?:export\s+)?interface\s+([$A-Za-z_][\w$]*)\b`, "interface"),
		spec(`^\s*(?:public\s+|private\s+|protected\s+|static\s+|async\s+|get\s+|set\s+)*([$A-Za-z_][\w$]*)\s*\([^)]*\)\s*[:{]`, "method"),
	},
	"go": {
		spec(`^\s*func\s+(?:\([^)]*\)\s*)?([A-Za-z_]\w*)\s*\(`, "function"),
		spec(`^\s*type\s+([A-Za-z_]\w*)\s+(?:struct|interface)\b`, "type"),
	},
	"rust": {
		spec(`^\s*(?:pub(?:\([^)]*\))?\s+)?(?:async\s+)?fn\s+([A-Za-z_]\w*)\s*[<(]`, "function"),
		spec(`^\s*(?:pub(?:\([^)]*\))?\s+)?struct\s+([A-Za-z_]\w*)\b`, "type"),
		spec(`^\s*(?:pub(?:\([^)]*\))?\s+)?enum\s+([A-Za-z_]\w*)\b`, "enum"),
		spec(`^\s*(?:pub(?:\([^)]*\))?\s+)?trait\s+([A-Za-z_]\w*)\b`, "trait"),
	},
	"java": {
		spec(`^\s*(?:public|protected|private|abstract|final|static|\s)+class\s+([A-Za-z_]\w*)\b`, "class"),
		spec(`^\s*(?:public|protected|private|static|final|synchronized|native|abstract|\s)+[\w<>\[\], ?]+\s+([A-Za-z_]\w*)\s*\([^;]*\)\s*\{?\s*$`, "method"),
	},
	"kotlin": {
		spec(`^\s*(?:public|private|protected|internal|open|override|suspend|\s)*fun\s+([A-Za-z_]\w*)\s*\(`, "function"),
		spec(`^\s*(?:data\s+|sealed\s+|open\s+)?class\s+([A-Za-z_]\w*)\b`, "class"),
		spec(`^\s*interface\s+([A-Za-z_]\w*)\b`, "interface"),
	},
	"swift": {
		spec(`^\s*(?:public|private|internal|open|static|class|mutating|\s)*func\s+([A-Za-z_]\w*)\s*\(`, "function"),
		spec(`^\s*(?:public|private|internal|open|\s)*(?:class|struct|enum|protocol)\s+([A-Za-z_]\w*)\b`, "type"),
	},
	"csharp": {
		spec(`^\s*(?:public|private|protected|internal|static|async|virtual|override|sealed|partial|\s)+class\s+([A-Za-z_]\w*)\b`, "class"),
		spec(`^\s*(?:public|private|protected|internal|static|async|virtual|override|sealed|\s)+[\w<>\[\], ?]+\s+([A-Za-z_]\w*)\s*\([^;]*\)\s*\{?\s*$`, "method"),
	},
	"php": {
		spec(`^\s*(?:public|protected|private|static|\s)*function\s+([A-Za-z_]\w*)\s*\(`, "function"),
		spec(`^\s*(?:abstract\s+|final\s+)?class\s+([A-Za-z_]\w*)\b`, "class"),
	},
	"elixir": {
		spec(`^\s*defmodule\s+([A-Za-z_]\w*(?:\.[A-Za-z_]\w*)*)\s+do\b`, "module"),
		spec(`^\s*defp?\s+([A-Za-z_]\w*[!?]?)\b`, "function"),
	},
	"lua": {
		spec(`^\s*(?:local\s+)?function\s+([A-Za-z_]\w*(?:[.:]\w+)*)\s*\(`, "function"),
		spec(`^\s*([A-Za-z_]\w*(?:[.:]\w+)*)\s*=\s*function\s*\(`, "function"),
	},
	"shell": {
		spec(`^\s*(?:function\s+)?([A-Za-z_][\w.-]*)\s*\(\)\s*\{?`, "function"),
		spec(`^\s*function\s+([A-Za-z_][\w.-]*)\b`, "function"),
	},
	"elisp": {
		spec(`^\s*\((?:cl-)?defun\s+([-A-Za-z0-9_+*/!?<>=]+)\b`, "function"),
		spec(`^\s*\(defmacro\s+([-A-Za-z0-9_+*/!?<>=]+)\b`, "function"),
		spec(`^\s*\(def(?:var|custom|const)\s+([-A-Za-z0-9_+*/!?<>=]+)\b`, "constant"),
	},
	"scheme": {
		spec(`^\s*\(define\s+\(?([-A-Za-z0-9_+*/!?<>=]+)\b`, "function"),
	},
	"clojure": {
		spec(`^\s*\(defn-?\s+([-A-Za-z0-9_+*/!?<>=]+)\b`, "function"),
		spec(`^\s*\(def(?:macro|record|protocol|multi)?\s+([-A-Za-z0-9_+*/!?<>=]+)\b`, "constant"),
	},
	"c": {
		spec(`^\s*(?:[A-Za-z_][\w\s*]+)\s+([A-Za-z_]\w*)\s*\([^;{}]*\)\s*(?:\{|$)`, "function"),
	},
	"cpp": {
		spec(`^\s*(?:template\s*<[^>]+>\s*)?(?:[\w:<>,~*&\s]+)\s+([A-Za-z_~]\w*)\s*\([^;{}]*\)\s*(?:const\s*)?(?:noexcept\s*)?(?:->\s*[^{]+)?\s*(?:\{|$)`, "function"),
		spec(`^\s*(?:class|struct)\s+([A-Za-z_]\w*)\b`, "type"),
	},
}

var skipSymbolNames = map[string]bool{
	"catch":  true,
	"else":   true,
	"for":    true,
	"if":     true,
	"switch": true,
	"while":  true,
	"with":   true,
}

func spec(pattern, kind string) symbolSpec {
	return symbolSpec{re: regexp.MustCompile(pattern), kind: kind}
}

func extractSymbols(path, language string, lines []string) []symbol {
	patterns := symbolPatterns[language]
	if len(patterns) == 0 {
		return nil
	}
	var out []symbol
	seen := map[string]bool{}
	for i, line := range lines {
		for _, pattern := range patterns {
			match := pattern.re.FindStringSubmatchIndex(line)
			if match == nil || len(match) < 4 {
				continue
			}
			name := line[match[2]:match[3]]
			if skipSymbolNames[name] {
				continue
			}
			key := name + ":" + strconv.Itoa(i+1) + ":" + pattern.kind
			if seen[key] {
				continue
			}
			seen[key] = true
			start := i - 2
			if start < 0 {
				start = 0
			}
			end := i + 3
			if end > len(lines) {
				end = len(lines)
			}
			out = append(out, symbol{
				path:      path,
				language:  language,
				kind:      pattern.kind,
				name:      name,
				line:      i + 1,
				column:    match[2] + 1,
				signature: truncate(strings.TrimSpace(line), 500),
				context:   truncate(strings.Join(lines[start:end], "\n"), 2000),
			})
			break
		}
	}
	return out
}
