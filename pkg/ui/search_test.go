package ui

import (
	"reflect"
	"testing"
)

func TestFindHits(t *testing.T) {
	lines := []string{
		"the quick brown fox",
		"THE lazy dog",
		"no match here",
		"catcat", // overlapping-adjacent repeats
	}
	cases := []struct {
		name  string
		query string
		want  []searchHit
	}{
		{
			name:  "case insensitive across lines",
			query: "the",
			want:  []searchHit{{line: 0, col: 0}, {line: 1, col: 0}},
		},
		{
			name:  "multiple hits same line",
			query: "cat",
			want:  []searchHit{{line: 3, col: 0}, {line: 3, col: 3}},
		},
		{
			name:  "no match",
			query: "zzz",
			want:  nil,
		},
		{
			name:  "empty query",
			query: "",
			want:  nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := findHits(lines, tc.query)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("findHits(%q) = %v, want %v", tc.query, got, tc.want)
			}
		})
	}
}

func TestRunesEqual(t *testing.T) {
	if !runesEqual([]rune("abc"), []rune("abc")) {
		t.Fatal("expected equal runes to compare equal")
	}
	if runesEqual([]rune("abc"), []rune("abd")) {
		t.Fatal("expected different runes to compare unequal")
	}
	if runesEqual([]rune("ab"), []rune("abc")) {
		t.Fatal("expected different lengths to compare unequal")
	}
}
