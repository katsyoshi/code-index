package main

import "testing"

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
	symbols := extractSymbols("sample.go", "go", splitLines(source))

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
	symbols := extractSymbols("broken.go", "go", splitLines(source))

	assertSymbol(t, symbols, "function", "stillIndexed", 3)
}

func TestExtractRubySymbolsStillUsesRegex(t *testing.T) {
	source := `module Sample
  class Worker
    def perform!
    end
  end
end
`
	symbols := extractSymbols("worker.rb", "ruby", splitLines(source))

	assertSymbol(t, symbols, "module", "Sample", 1)
	assertSymbol(t, symbols, "class", "Worker", 2)
	assertSymbol(t, symbols, "method", "perform", 3)
}

func assertSymbol(t *testing.T, symbols []symbol, kind, name string, line int) {
	t.Helper()
	for _, symbol := range symbols {
		if symbol.kind == kind && symbol.name == name && symbol.line == line {
			return
		}
	}
	t.Fatalf("symbol %s %s at line %d not found in %+v", kind, name, line, symbols)
}

func assertNoSymbol(t *testing.T, symbols []symbol, kind, name string) {
	t.Helper()
	for _, symbol := range symbols {
		if symbol.kind == kind && symbol.name == name {
			t.Fatalf("unexpected symbol %s %s found in %+v", kind, name, symbols)
		}
	}
}
