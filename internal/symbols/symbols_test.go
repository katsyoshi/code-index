package symbols

import (
	"strings"
	"testing"
)

func TestExtractGoSymbolsUsesAST(t *testing.T) {
	source := `package sample

const answer = 42

var (
	count int
	ignored = func() {}
)

type Service struct{}
type Alias = string

func NewService() *Service {
	return &Service{}
}

func (s *Service) Run(ctx context.Context) error {
	return nil
}
`
	symbols := Extract("sample.go", "go", splitLines(source))

	assertSymbol(t, symbols, "constant", "answer", 3)
	assertSymbol(t, symbols, "variable", "count", 6)
	assertSymbol(t, symbols, "variable", "ignored", 7)
	assertSymbol(t, symbols, "type", "Service", 10)
	assertSymbol(t, symbols, "type", "Alias", 11)
	assertSymbol(t, symbols, "function", "NewService", 13)
	assertSymbol(t, symbols, "method", "Run", 17)
	assertNoSymbol(t, symbols, "function", "ignored")
}

func TestExtractGoSymbolsHandlesPartialParse(t *testing.T) {
	source := `package sample

func stillIndexed() {
`
	symbols := Extract("broken.go", "go", splitLines(source))

	assertSymbol(t, symbols, "function", "stillIndexed", 3)
}

func TestRegexRubySymbolsFallback(t *testing.T) {
	source := `module Sample
  class Worker
    def perform!
    end
  end
end
`
	symbols, ok := regexSymbolExtractor{}.extract("worker.rb", "ruby", splitLines(source))
	if !ok {
		t.Fatal("regex ruby extraction failed")
	}

	assertSymbol(t, symbols, "module", "Sample", 1)
	assertSymbol(t, symbols, "class", "Worker", 2)
	assertSymbol(t, symbols, "method", "perform", 3)
}

func TestParseRubyPrismDumpSymbols(t *testing.T) {
	source := `module Sample
  class Worker
    CONST = 1

    def self.build
    end

    def perform!
    end
  end
end
`
	dump := `@ ProgramNode (location: (1,0)-(11,3))
        +-- @ ModuleNode (location: (1,0)-(11,3))
            +-- @ ClassNode (location: (2,2)-(10,5))
                +-- @ ConstantWriteNode (location: (3,4)-(3,13))
                |   +-- name: :CONST
                |   +-- name_loc: (3,4)-(3,9) = "CONST"
                +-- @ DefNode (location: (5,4)-(6,7))
                |   +-- name: :build
                |   +-- name_loc: (5,13)-(5,18) = "build"
                |   +-- receiver:
                |   |   @ SelfNode (location: (5,8)-(5,12))
                |   +-- parameters: nil
                +-- @ DefNode (location: (8,4)-(9,7))
                    +-- name: :perform!
                    +-- name_loc: (8,8)-(8,16) = "perform!"
                    +-- receiver: nil
                    +-- parameters: nil
`
	symbols, ok := parseRubyPrismDump("worker.rb", "ruby", splitLines(source), dump)
	if !ok {
		t.Fatal("parseRubyPrismDump failed")
	}

	assertSymbol(t, symbols, "module", "Sample", 1)
	assertSymbol(t, symbols, "class", "Worker", 2)
	assertSymbol(t, symbols, "constant", "CONST", 3)
	assertSymbol(t, symbols, "method", "self.build", 5)
	assertSymbol(t, symbols, "method", "perform!", 8)
}

func TestParseRubyParseYDumpSymbols(t *testing.T) {
	source := `module Sample
  class Worker
    CONST = 1

    def self.build
    end

    def perform!
    end
  end
end
`
	dump := `# @ NODE_SCOPE (id: 1, line: 1, location: (1,0)-(11,3))
#     @ NODE_MODULE (id: 2, line: 1, location: (1,0)-(11,3))*
#           @ NODE_CLASS (id: 3, line: 2, location: (2,2)-(10,5))*
#                 @ NODE_CDECL (id: 4, line: 3, location: (3,4)-(3,13))*
#                 +- nd_vid: :CONST
#                 @ NODE_DEFS (id: 5, line: 5, location: (5,4)-(6,7))*
#                 +- nd_mid: :build
#                 @ NODE_DEFN (id: 6, line: 8, location: (8,4)-(9,7))*
#                 +- nd_mid: :perform!
`
	symbols, ok := parseRubyParseYDump("worker.rb", "ruby", splitLines(source), dump)
	if !ok {
		t.Fatal("parseRubyParseYDump failed")
	}

	assertSymbol(t, symbols, "module", "Sample", 1)
	assertSymbol(t, symbols, "class", "Worker", 2)
	assertSymbol(t, symbols, "constant", "CONST", 3)
	assertSymbol(t, symbols, "method", "self.build", 5)
	assertSymbol(t, symbols, "method", "perform!", 8)
}

func assertSymbol(t *testing.T, symbols []Symbol, kind, name string, line int) {
	t.Helper()
	for _, symbol := range symbols {
		if symbol.Kind == kind && symbol.Name == name && symbol.Line == line {
			return
		}
	}
	t.Fatalf("symbol %s %s at line %d not found in %+v", kind, name, line, symbols)
}

func assertNoSymbol(t *testing.T, symbols []Symbol, kind, name string) {
	t.Helper()
	for _, symbol := range symbols {
		if symbol.Kind == kind && symbol.Name == name {
			t.Fatalf("unexpected symbol %s %s found in %+v", kind, name, symbols)
		}
	}
}

func splitLines(text string) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	if text == "" {
		return nil
	}
	lines := strings.Split(text, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}
