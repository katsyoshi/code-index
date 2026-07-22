package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"unicode"
)

type stringListFlag []string

func (values *stringListFlag) String() string {
	return strings.Join(*values, ",")
}

func (values *stringListFlag) Set(value string) error {
	if value == "" {
		return errors.New("kind must not be empty")
	}
	*values = append(*values, value)
	return nil
}

type refsQueryJSON struct {
	Name          string   `json:"name"`
	Kinds         []string `json:"kinds"`
	Language      *string  `json:"language"`
	CaseSensitive bool     `json:"case_sensitive"`
	Limit         int      `json:"limit"`
}

type refsScopeJSON struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
	Line int    `json:"line"`
}

type refsCandidateJSON struct {
	Path     string          `json:"path"`
	Line     int             `json:"line"`
	Language *string         `json:"language"`
	Text     string          `json:"text"`
	Scope    []refsScopeJSON `json:"scope"`
}

type refsJSONResult struct {
	Query       refsQueryJSON       `json:"query"`
	Definitions []defsJSONRow       `json:"definitions"`
	Candidates  []refsCandidateJSON `json:"candidates"`
}

type refsRawCandidate struct {
	Path     string  `json:"path"`
	Line     int     `json:"line"`
	Language *string `json:"language"`
	Text     string  `json:"text"`
}

type refsScopeRow struct {
	Path    string `json:"path"`
	Line    int    `json:"line"`
	EndLine int    `json:"end_line"`
	Kind    string `json:"kind"`
	Name    string `json:"name"`
}

func cmdRefs(args []string) error {
	fs := flag.NewFlagSet("refs", flag.ExitOnError)
	root := fs.String("root", "", "repository root for default database path")
	db := fs.String("db", "", "database path")
	language := fs.String("language", "", "language filter")
	ignoreCase := fs.Bool("ignore-case", false, "match identifiers without case sensitivity")
	limit := fs.Int("limit", 100, "maximum reference candidates")
	formatFlag := fs.String("format", "text", "output format: text or json")
	var kinds stringListFlag
	fs.Var(&kinds, "kind", "symbol kind filter; may be repeated")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 || fs.Arg(0) == "" || *limit <= 0 {
		return errors.New(commandUsage("refs"))
	}
	format, err := parseOutputFormat(*formatFlag)
	if err != nil {
		return err
	}
	dbPath, _, err := resolveDB(*db, *root)
	if err != nil {
		return err
	}
	name := fs.Arg(0)
	normalizedKinds := sortStringsUnique([]string(kinds))
	result, err := loadRefsResult(dbPath, name, normalizedKinds, *language, !*ignoreCase, *limit)
	if err != nil {
		return err
	}
	if format == outputFormatJSON {
		return writeJSON(os.Stdout, result)
	}
	writeRefsText(result)
	return nil
}

func loadRefsResult(db, name string, kinds []string, language string, caseSensitive bool, limit int) (refsJSONResult, error) {
	query := refsQueryJSON{
		Name:          name,
		Kinds:         append([]string{}, kinds...),
		Language:      optionalString(language),
		CaseSensitive: caseSensitive,
		Limit:         limit,
	}
	result := refsJSONResult{
		Query:       query,
		Definitions: make([]defsJSONRow, 0),
		Candidates:  make([]refsCandidateJSON, 0),
	}
	allDefinitions, err := loadExactDefinitions(db, name, language, caseSensitive)
	if err != nil {
		return result, err
	}
	definitionLines := make(map[string]bool, len(allDefinitions))
	for _, definition := range allDefinitions {
		definitionLines[definition.Path+":"+fmt.Sprint(definition.Line)] = true
		if len(kinds) == 0 || containsString(kinds, definition.Kind) {
			result.Definitions = append(result.Definitions, definition)
		}
	}
	candidates, err := loadReferenceCandidates(db, name, language, caseSensitive, limit, definitionLines)
	if err != nil {
		return result, err
	}
	if err := attachReferenceScopes(db, candidates); err != nil {
		return result, err
	}
	result.Candidates = candidates
	return result, nil
}

func loadExactDefinitions(db, name, language string, caseSensitive bool) ([]defsJSONRow, error) {
	comparison := "name = " + quote(name)
	if !caseSensitive {
		comparison += " collate nocase"
	}
	if language != "" {
		comparison += " and language = " + quote(language)
	}
	sql := "select path, line, kind, name, language, signature from symbols where " + comparison + " order by path, line, column, name"
	rows := make([]defsJSONRow, 0)
	if err := sqliteJSONQuery(db, sql, &rows); err != nil {
		return nil, err
	}
	return rows, nil
}

func loadReferenceCandidates(db, name, language string, caseSensitive bool, limit int, definitionLines map[string]bool) ([]refsCandidateJSON, error) {
	const batchSize = 500
	result := make([]refsCandidateJSON, 0, limit)
	for offset := 0; len(result) < limit; offset += batchSize {
		where := "instr(lines.text, " + quote(name) + ") > 0"
		if !caseSensitive {
			if isASCII(name) {
				where = "instr(lower(lines.text), lower(" + quote(name) + ")) > 0"
			} else {
				// SQLite's built-in lower() only guarantees ASCII case folding.
				// Scan all filtered lines and apply strings.EqualFold in Go for
				// non-ASCII identifiers.
				where = "1 = 1"
			}
		}
		if language != "" {
			where += " and files.language = " + quote(language)
		}
		sql := fmt.Sprintf(
			"select files.path as path, lines.line as line, files.language as language, lines.text as text from lines join files on files.id = lines.file_id where %s order by files.path, lines.line limit %d offset %d",
			where,
			batchSize,
			offset,
		)
		raw := make([]refsRawCandidate, 0)
		if err := sqliteJSONQuery(db, sql, &raw); err != nil {
			return nil, err
		}
		for _, row := range raw {
			if definitionLines[row.Path+":"+fmt.Sprint(row.Line)] || !containsIdentifier(row.Text, name, caseSensitive) {
				continue
			}
			result = append(result, refsCandidateJSON{
				Path:     row.Path,
				Line:     row.Line,
				Language: row.Language,
				Text:     row.Text,
				Scope:    make([]refsScopeJSON, 0),
			})
			if len(result) == limit {
				break
			}
		}
		if len(raw) < batchSize {
			break
		}
	}
	return result, nil
}

func attachReferenceScopes(db string, candidates []refsCandidateJSON) error {
	paths := make([]string, 0)
	seen := map[string]bool{}
	for _, candidate := range candidates {
		if !seen[candidate.Path] {
			seen[candidate.Path] = true
			paths = append(paths, candidate.Path)
		}
	}
	if len(paths) == 0 {
		return nil
	}
	quotedPaths := make([]string, len(paths))
	for i, path := range paths {
		quotedPaths[i] = quote(path)
	}
	sql := "select path, line, end_line, kind, name from symbols where end_line > line and path in (" + strings.Join(quotedPaths, ",") + ") order by path, line, end_line desc, column, name"
	rows := make([]refsScopeRow, 0)
	if err := sqliteJSONQuery(db, sql, &rows); err != nil {
		return err
	}
	for i := range candidates {
		for _, row := range rows {
			if row.Path == candidates[i].Path && row.Line <= candidates[i].Line && row.EndLine >= candidates[i].Line && isScopeKind(row.Kind) {
				candidates[i].Scope = append(candidates[i].Scope, refsScopeJSON{Kind: row.Kind, Name: row.Name, Line: row.Line})
			}
		}
	}
	return nil
}

func containsIdentifier(line, name string, caseSensitive bool) bool {
	lineRunes := []rune(line)
	nameRunes := []rune(name)
	if len(nameRunes) == 0 || len(nameRunes) > len(lineRunes) {
		return false
	}
	for i := 0; i+len(nameRunes) <= len(lineRunes); i++ {
		candidate := string(lineRunes[i : i+len(nameRunes)])
		matches := candidate == name
		if !caseSensitive {
			matches = strings.EqualFold(candidate, name)
		}
		if !matches {
			continue
		}
		if i > 0 && isIdentifierRune(lineRunes[i-1]) {
			continue
		}
		end := i + len(nameRunes)
		if end < len(lineRunes) && isIdentifierRune(lineRunes[end]) {
			continue
		}
		return true
	}
	return false
}

func isIdentifierRune(value rune) bool {
	return value == '_' || value == '$' || unicode.IsLetter(value) || unicode.IsDigit(value)
}

func isASCII(value string) bool {
	for _, char := range value {
		if char > unicode.MaxASCII {
			return false
		}
	}
	return true
}

func isScopeKind(kind string) bool {
	switch kind {
	case "module", "class", "type", "interface", "trait", "enum", "method", "function":
		return true
	default:
		return false
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func writeRefsText(result refsJSONResult) {
	kinds := "all"
	if len(result.Query.Kinds) > 0 {
		kinds = strings.Join(result.Query.Kinds, ", ")
	}
	caseLabel := "case-sensitive"
	if !result.Query.CaseSensitive {
		caseLabel = "ignore-case"
	}
	fmt.Printf("query: %s (kinds: %s, %s)\n\n", result.Query.Name, kinds, caseLabel)
	fmt.Println("definitions:")
	for _, definition := range result.Definitions {
		fmt.Printf("  %s:%d  %s %s\n", definition.Path, definition.Line, definition.Kind, definition.Name)
	}
	fmt.Println()
	fmt.Println("references:")
	currentPath := ""
	for _, candidate := range result.Candidates {
		if candidate.Path != currentPath {
			if currentPath != "" {
				fmt.Println()
			}
			currentPath = candidate.Path
			fmt.Printf("  %s\n", candidate.Path)
		}
		if len(candidate.Scope) > 0 {
			parts := make([]string, len(candidate.Scope))
			for i, scope := range candidate.Scope {
				parts[i] = scope.Kind + " " + scope.Name
			}
			fmt.Printf("    [%s]\n", strings.Join(parts, " > "))
		}
		fmt.Printf("      %d  %s\n", candidate.Line, strings.TrimSpace(candidate.Text))
	}
}

func sortStringsUnique(values []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}
