package symbols

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
)

type goSymbolExtractor struct{}

func (goSymbolExtractor) extract(path, language string, lines []string) ([]Symbol, bool) {
	source := strings.Join(lines, "\n")
	fileset := token.NewFileSet()
	file, err := parser.ParseFile(fileset, path, source, parser.SkipObjectResolution)
	if file == nil {
		return nil, false
	}
	if err != nil && len(file.Decls) == 0 {
		return nil, false
	}

	var out []Symbol
	for _, decl := range file.Decls {
		switch decl := decl.(type) {
		case *ast.FuncDecl:
			kind := "function"
			if decl.Recv != nil {
				kind = "method"
			}
			out = append(out, goSymbol(path, language, kind, decl.Name.Name, decl.Name.Pos(), decl.Pos(), decl.Type.End(), fileset, lines))
		case *ast.GenDecl:
			out = append(out, goGenDeclSymbols(path, language, decl, fileset, lines)...)
		}
	}
	return out, true
}

func goGenDeclSymbols(path, language string, decl *ast.GenDecl, fileset *token.FileSet, lines []string) []Symbol {
	var out []Symbol
	for _, spec := range decl.Specs {
		switch spec := spec.(type) {
		case *ast.TypeSpec:
			out = append(out, goSymbol(path, language, "type", spec.Name.Name, spec.Name.Pos(), spec.Pos(), spec.End(), fileset, lines))
		case *ast.ValueSpec:
			kind := "variable"
			if decl.Tok == token.CONST {
				kind = "constant"
			}
			for _, name := range spec.Names {
				if name.Name == "_" {
					continue
				}
				out = append(out, goSymbol(path, language, kind, name.Name, name.Pos(), spec.Pos(), spec.End(), fileset, lines))
			}
		}
	}
	return out
}

func goSymbol(path, language, kind, name string, namePos, signatureStart, signatureEnd token.Pos, fileset *token.FileSet, lines []string) Symbol {
	position := fileset.Position(namePos)
	signature := goSignature(fileset, signatureStart, signatureEnd, lines)
	return buildSymbol(path, language, kind, name, position.Line, position.Column, signature, lines)
}

func goSignature(fileset *token.FileSet, start, end token.Pos, lines []string) string {
	startLine := fileset.Position(start).Line
	endLine := fileset.Position(end).Line
	if startLine < 1 {
		startLine = 1
	}
	if endLine < startLine {
		endLine = startLine
	}
	if endLine > len(lines) {
		endLine = len(lines)
	}
	if startLine > len(lines) {
		return ""
	}
	const maxSignatureLines = 12
	if endLine-startLine+1 > maxSignatureLines {
		endLine = startLine + maxSignatureLines - 1
	}
	return strings.Join(lines[startLine-1:endLine], "\n")
}
