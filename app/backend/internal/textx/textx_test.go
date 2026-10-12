package textx

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestRunes(t *testing.T) {
	cases := []struct {
		in   string
		n    int
		want string
	}{
		{"", 3, ""},
		{"abc", 3, "abc"},
		{"abcd", 3, "abc"},
		{"привет", 3, "при"},
		{"привет", 6, "привет"},
		{"a€b", 2, "a€"},
		{"abc", 0, ""},
	}
	for _, c := range cases {
		if got := Runes(c.in, c.n); got != c.want {
			t.Errorf("Runes(%q, %d) = %q, want %q", c.in, c.n, got, c.want)
		}
	}
	long := strings.Repeat("ж", 4001)
	if got := Runes(long, 4000); utf8.RuneCountInString(got) != 4000 || !utf8.ValidString(got) {
		t.Errorf("4001 Cyrillic letters cut to %d runes", utf8.RuneCountInString(got))
	}
}

func TestBytes(t *testing.T) {
	cases := []struct {
		in   string
		n    int
		want string
	}{
		{"abc", 5, "abc"},
		{"abcd", 3, "abc"},
		{"привет", 3, "п"},
		{"привет", 4, "пр"},
		{"a€b", 3, "a"},
		{"a€b", 4, "a€"},
	}
	for _, c := range cases {
		got := Bytes(c.in, c.n)
		if got != c.want || !utf8.ValidString(got) {
			t.Errorf("Bytes(%q, %d) = %q, want %q", c.in, c.n, got, c.want)
		}
	}
}
