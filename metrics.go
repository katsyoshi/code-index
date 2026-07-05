package main

import "strings"

type fileMetrics struct {
	lineCount    int
	blankLines   int
	commentLines int
	codeLines    int
	symbolCount  int
}

func computeFileMetrics(language string, lines []string, symbolCount int) fileMetrics {
	metrics := fileMetrics{
		lineCount:   len(lines),
		symbolCount: symbolCount,
	}
	var blockEnd string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			metrics.blankLines++
			continue
		}
		if isCommentOnlyLine(language, trimmed, &blockEnd) {
			metrics.commentLines++
			continue
		}
		metrics.codeLines++
	}
	return metrics
}

func isCommentOnlyLine(language, trimmed string, blockEnd *string) bool {
	if *blockEnd != "" {
		if strings.Contains(trimmed, *blockEnd) {
			*blockEnd = ""
		}
		return true
	}

	if start, end, ok := blockCommentDelimiters(language); ok && strings.HasPrefix(trimmed, start) {
		if !strings.Contains(trimmed[len(start):], end) {
			*blockEnd = end
		}
		return true
	}

	for _, prefix := range lineCommentPrefixes(language) {
		if strings.HasPrefix(trimmed, prefix) {
			return true
		}
	}

	if start, end, ok := blockCommentDelimiters(language); ok {
		if index := strings.Index(trimmed, start); index >= 0 && !strings.Contains(trimmed[index+len(start):], end) {
			*blockEnd = end
		}
	}
	return false
}

func lineCommentPrefixes(language string) []string {
	switch language {
	case "clojure", "elisp", "scheme":
		return []string{";"}
	case "haskell", "lua":
		return []string{"--"}
	case "c", "cpp", "csharp", "go", "java", "javascript", "kotlin", "rust", "scala", "swift", "typescript":
		return []string{"//"}
	case "php":
		return []string{"//", "#"}
	case "dockerfile", "elixir", "make", "python", "ruby", "shell":
		return []string{"#"}
	default:
		return nil
	}
}

func blockCommentDelimiters(language string) (string, string, bool) {
	switch language {
	case "c", "cpp", "csharp", "css", "go", "java", "javascript", "kotlin", "php", "rust", "scala", "swift", "typescript":
		return "/*", "*/", true
	case "haskell":
		return "{-", "-}", true
	case "html", "vue":
		return "<!--", "-->", true
	case "lua":
		return "--[[", "]]", true
	case "ruby":
		return "=begin", "=end", true
	default:
		return "", "", false
	}
}
