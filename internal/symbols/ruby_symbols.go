package symbols

import (
	"context"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const rubyDumpTimeout = 5 * time.Second

var (
	rubyCommandOnce sync.Once
	rubyCommandPath string
	rubyCommandOK   bool

	rubyPrismNodeRE  = regexp.MustCompile(`@\s+(Module|Class|Def|ConstantWrite)Node \(location: \((\d+),(\d+)\)-\((\d+),(\d+)\)\)`)
	rubyParseYNodeRE = regexp.MustCompile(`@\s+NODE_(MODULE|CLASS|DEFN|DEFS|CDECL)\s+\(.*line: \d+, location: \((\d+),(\d+)\)-\((\d+),(\d+)\)`)
	rubyClassNameRE  = regexp.MustCompile(`\bclass\s+([A-Z]\w*(?:::[A-Z]\w*)*)`)
	rubyModuleNameRE = regexp.MustCompile(`\bmodule\s+([A-Z]\w*(?:::[A-Z]\w*)*)`)
	rubyDumpNameRE   = regexp.MustCompile(`\+-- name: :(.+)$`)
	rubyNameLocRE    = regexp.MustCompile(`\+-- name_loc: \((\d+),(\d+)\)-\((\d+),(\d+)\)`)
	rubyParseYNameRE = regexp.MustCompile(`\+- nd_(?:mid|vid): :(.+)$`)
)

type rubySymbolExtractor struct{}

func (rubySymbolExtractor) extract(path, language string, lines []string) ([]Symbol, bool) {
	ruby, ok := rubyCommand()
	if !ok {
		return nil, false
	}
	if dump, ok := rubyDump(ruby, []string{"--parser=prism", "--dump=parsetree", "-"}, lines); ok {
		if symbols, ok := parseRubyPrismDump(path, language, lines, dump); ok {
			return symbols, true
		}
	}
	if dump, ok := rubyDump(ruby, []string{"--dump=parsetree", "-"}, lines); ok {
		if symbols, ok := parseRubyParseYDump(path, language, lines, dump); ok {
			return symbols, true
		}
	}
	return nil, false
}

func rubyDump(ruby string, args []string, lines []string) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), rubyDumpTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, ruby, args...)
	cmd.Stdin = strings.NewReader(strings.Join(lines, "\n"))
	out, err := cmd.Output()
	if err != nil || ctx.Err() != nil {
		return "", false
	}
	return string(out), true
}

func rubyCommand() (string, bool) {
	rubyCommandOnce.Do(func() {
		path, err := exec.LookPath("ruby")
		if err == nil {
			rubyCommandPath = path
			rubyCommandOK = true
		}
	})
	return rubyCommandPath, rubyCommandOK
}

func parseRubyPrismDump(path, language string, sourceLines []string, dump string) ([]Symbol, bool) {
	if !strings.Contains(dump, "@ ProgramNode") {
		return nil, false
	}
	var out []Symbol
	dumpLines := strings.Split(dump, "\n")
	for i, line := range dumpLines {
		node, startLine, startColumn, ok := rubyDumpNode(line)
		if !ok {
			continue
		}
		switch node {
		case "Module":
			name, column := rubyClassOrModuleName(sourceLines, startLine, startColumn, "module")
			if name != "" {
				out = append(out, buildSymbol(path, language, "module", name, startLine, column, sourceLine(sourceLines, startLine), sourceLines))
			}
		case "Class":
			name, column := rubyClassOrModuleName(sourceLines, startLine, startColumn, "class")
			if name != "" {
				out = append(out, buildSymbol(path, language, "class", name, startLine, column, sourceLine(sourceLines, startLine), sourceLines))
			}
		case "Def":
			if sym, ok := rubyDefSymbol(path, language, sourceLines, dumpLines, i); ok {
				out = append(out, sym)
			}
		case "ConstantWrite":
			if sym, ok := rubyConstantSymbol(path, language, sourceLines, dumpLines, i); ok {
				out = append(out, sym)
			}
		}
	}
	return out, true
}

func rubyDumpNode(line string) (string, int, int, bool) {
	match := rubyPrismNodeRE.FindStringSubmatch(line)
	if match == nil {
		return "", 0, 0, false
	}
	startLine, err := strconv.Atoi(match[2])
	if err != nil {
		return "", 0, 0, false
	}
	startColumn, err := strconv.Atoi(match[3])
	if err != nil {
		return "", 0, 0, false
	}
	return match[1], startLine, startColumn, true
}

func parseRubyParseYDump(path, language string, sourceLines []string, dump string) ([]Symbol, bool) {
	if !strings.Contains(dump, "@ NODE_") {
		return nil, false
	}
	var out []Symbol
	dumpLines := strings.Split(dump, "\n")
	for i, line := range dumpLines {
		node, startLine, startColumn, ok := rubyParseYNode(line)
		if !ok {
			continue
		}
		switch node {
		case "MODULE":
			name, column := rubyClassOrModuleName(sourceLines, startLine, startColumn, "module")
			if name != "" {
				out = append(out, buildSymbol(path, language, "module", name, startLine, column, sourceLine(sourceLines, startLine), sourceLines))
			}
		case "CLASS":
			name, column := rubyClassOrModuleName(sourceLines, startLine, startColumn, "class")
			if name != "" {
				out = append(out, buildSymbol(path, language, "class", name, startLine, column, sourceLine(sourceLines, startLine), sourceLines))
			}
		case "DEFN", "DEFS":
			if sym, ok := rubyParseYMethodSymbol(path, language, sourceLines, dumpLines, i, startLine, startColumn, node == "DEFS"); ok {
				out = append(out, sym)
			}
		case "CDECL":
			if sym, ok := rubyParseYConstantSymbol(path, language, sourceLines, dumpLines, i, startLine, startColumn); ok {
				out = append(out, sym)
			}
		}
	}
	return out, true
}

func rubyParseYNode(line string) (string, int, int, bool) {
	match := rubyParseYNodeRE.FindStringSubmatch(line)
	if match == nil {
		return "", 0, 0, false
	}
	startLine, err := strconv.Atoi(match[2])
	if err != nil {
		return "", 0, 0, false
	}
	startColumn, err := strconv.Atoi(match[3])
	if err != nil {
		return "", 0, 0, false
	}
	return match[1], startLine, startColumn, true
}

func rubyParseYMethodSymbol(path, language string, sourceLines, dumpLines []string, nodeLine, startLine, startColumn int, hasReceiver bool) (Symbol, bool) {
	name, ok := rubyParseYName(dumpLines, nodeLine+1, 16)
	if !ok {
		return Symbol{}, false
	}
	column := rubyNameColumn(sourceLines, name, startLine, startColumn)
	if column == 0 {
		return Symbol{}, false
	}
	if hasReceiver {
		name = rubyQualifiedMethodName(sourceLines, name, startLine, column)
	}
	return buildSymbol(path, language, "method", name, startLine, column, sourceLine(sourceLines, startLine), sourceLines), true
}

func rubyParseYConstantSymbol(path, language string, sourceLines, dumpLines []string, nodeLine, startLine, startColumn int) (Symbol, bool) {
	name, ok := rubyParseYName(dumpLines, nodeLine+1, 8)
	if !ok {
		return Symbol{}, false
	}
	column := rubyNameColumn(sourceLines, name, startLine, startColumn)
	if column == 0 {
		return Symbol{}, false
	}
	return buildSymbol(path, language, "constant", name, startLine, column, sourceLine(sourceLines, startLine), sourceLines), true
}

func rubyParseYName(lines []string, start, limit int) (string, bool) {
	end := start + limit
	if end > len(lines) {
		end = len(lines)
	}
	for i := start; i < end; i++ {
		if match := rubyParseYNameRE.FindStringSubmatch(lines[i]); match != nil {
			return strings.TrimSpace(match[1]), true
		}
	}
	return "", false
}

func rubyClassOrModuleName(lines []string, line, startColumn int, keyword string) (string, int) {
	source := sourceLine(lines, line)
	if startColumn > len(source) {
		return "", 0
	}
	segment := source[startColumn:]
	nameRE := rubyClassNameRE
	if keyword == "module" {
		nameRE = rubyModuleNameRE
	}
	match := nameRE.FindStringSubmatchIndex(segment)
	if match == nil {
		return "", 0
	}
	name := segment[match[2]:match[3]]
	return name, startColumn + match[2] + 1
}

func rubyDefSymbol(path, language string, sourceLines, dumpLines []string, nodeLine int) (Symbol, bool) {
	name, line, column, ok := rubyDumpNameAndLoc(dumpLines, nodeLine+1, 16)
	if !ok {
		return Symbol{}, false
	}
	hasReceiver := rubyDefHasReceiver(dumpLines, nodeLine+1, 16)
	if hasReceiver {
		name = rubyQualifiedMethodName(sourceLines, name, line, column)
	}
	return buildSymbol(path, language, "method", name, line, column, sourceLine(sourceLines, line), sourceLines), true
}

func rubyConstantSymbol(path, language string, sourceLines, dumpLines []string, nodeLine int) (Symbol, bool) {
	name, line, column, ok := rubyDumpNameAndLoc(dumpLines, nodeLine+1, 8)
	if !ok {
		return Symbol{}, false
	}
	return buildSymbol(path, language, "constant", name, line, column, sourceLine(sourceLines, line), sourceLines), true
}

func rubyDumpNameAndLoc(lines []string, start, limit int) (string, int, int, bool) {
	name := ""
	line := 0
	column := 0
	end := start + limit
	if end > len(lines) {
		end = len(lines)
	}
	for i := start; i < end; i++ {
		if name == "" {
			if match := rubyDumpNameRE.FindStringSubmatch(lines[i]); match != nil {
				name = strings.TrimSpace(match[1])
			}
		}
		if line == 0 {
			if match := rubyNameLocRE.FindStringSubmatch(lines[i]); match != nil {
				line, _ = strconv.Atoi(match[1])
				column, _ = strconv.Atoi(match[2])
				column++
			}
		}
		if name != "" && line > 0 {
			return name, line, column, true
		}
	}
	return "", 0, 0, false
}

func rubyDefHasReceiver(lines []string, start, limit int) bool {
	end := start + limit
	if end > len(lines) {
		end = len(lines)
	}
	for i := start; i < end; i++ {
		line := lines[i]
		if strings.Contains(line, "+-- parameters:") {
			return false
		}
		if strings.Contains(line, "+-- receiver: nil") {
			return false
		}
		if strings.Contains(line, "+-- receiver:") {
			return true
		}
	}
	return false
}

func rubyQualifiedMethodName(lines []string, name string, line, column int) string {
	source := sourceLine(lines, line)
	nameStart := column - 1
	if nameStart < 0 || nameStart > len(source) {
		return name
	}
	prefix := source[:nameStart]
	defIndex := strings.LastIndex(prefix, "def")
	if defIndex < 0 {
		return name
	}
	receiver := strings.TrimSpace(prefix[defIndex+len("def"):])
	if receiver == "" {
		return name
	}
	separator := "."
	if strings.HasSuffix(receiver, "::") {
		separator = "::"
		receiver = strings.TrimSuffix(receiver, "::")
	} else {
		receiver = strings.TrimSuffix(receiver, ".")
	}
	if receiver == "" {
		return name
	}
	return receiver + separator + name
}

func rubyNameColumn(lines []string, name string, line, startColumn int) int {
	source := sourceLine(lines, line)
	if startColumn > len(source) {
		return 0
	}
	index := strings.Index(source[startColumn:], name)
	if index < 0 {
		return 0
	}
	return startColumn + index + 1
}

func sourceLine(lines []string, line int) string {
	if line < 1 || line > len(lines) {
		return ""
	}
	return lines[line-1]
}
