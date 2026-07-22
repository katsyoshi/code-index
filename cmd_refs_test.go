package main

import "testing"

func TestContainsIdentifier(t *testing.T) {
	tests := []struct {
		name          string
		line          string
		query         string
		caseSensitive bool
		want          bool
	}{
		{name: "exact", line: "List<File> fileList", query: "List", caseSensitive: true, want: true},
		{name: "camel case suffix", line: "fileList", query: "List", caseSensitive: true, want: false},
		{name: "partial prefix", line: "ArrayList<File>", query: "List", caseSensitive: true, want: false},
		{name: "partial suffix", line: "Listing", query: "List", caseSensitive: true, want: false},
		{name: "case sensitive", line: "list", query: "List", caseSensitive: true, want: false},
		{name: "ignore case", line: "list", query: "List", caseSensitive: false, want: true},
		{name: "unicode boundary", line: "前List後", query: "List", caseSensitive: true, want: false},
		{name: "unicode identifier", line: "変数 = 変数 + 1", query: "変数", caseSensitive: true, want: true},
		{name: "unicode ignore case", line: "Äpfel", query: "äpfel", caseSensitive: false, want: true},
		{name: "dollar boundary", line: "$List = 1", query: "List", caseSensitive: true, want: false},
		{name: "ruby punctuation", line: "object.perform!", query: "perform!", caseSensitive: true, want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := containsIdentifier(test.line, test.query, test.caseSensitive); got != test.want {
				t.Fatalf("containsIdentifier(%q, %q, %t) = %t, want %t", test.line, test.query, test.caseSensitive, got, test.want)
			}
		})
	}
}

func TestSortStringsUnique(t *testing.T) {
	got := sortStringsUnique([]string{"method", "function", "method"})
	if len(got) != 2 || got[0] != "function" || got[1] != "method" {
		t.Fatalf("sortStringsUnique = %#v", got)
	}
}
