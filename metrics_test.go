package main

import "testing"

func TestComputeFileMetricsGo(t *testing.T) {
	lines := []string{
		"package main",
		"",
		"// comment",
		"func main() {",
		"  /* block",
		"     comment */",
		"}",
	}

	got := computeFileMetrics("go", lines, 1)
	want := fileMetrics{
		lineCount:    7,
		blankLines:   1,
		commentLines: 3,
		codeLines:    3,
		symbolCount:  1,
	}
	if got != want {
		t.Fatalf("computeFileMetrics() = %+v, want %+v", got, want)
	}
}

func TestComputeFileMetricsInlineBlockComment(t *testing.T) {
	lines := []string{
		"let value = 1 /* starts here",
		"   still comment",
		"*/",
		"// done",
	}

	got := computeFileMetrics("javascript", lines, 0)
	want := fileMetrics{
		lineCount:    4,
		commentLines: 3,
		codeLines:    1,
	}
	if got != want {
		t.Fatalf("computeFileMetrics() = %+v, want %+v", got, want)
	}
}
