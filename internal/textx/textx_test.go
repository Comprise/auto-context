package textx

import (
	"errors"
	"testing"
)

func TestLine(t *testing.T) {
	cases := []struct {
		in   string
		max  int
		want string
	}{
		{"a\nb\t c", 10, "a b c"},
		{"x\x1b[31mred\u202e", 20, "x [31mred"},
		{"  ", 5, ""},
		{"абвгд", 3, "абв…"},
	}
	for _, c := range cases {
		if got := Line(c.in, c.max); got != c.want {
			t.Errorf("Line(%q, %d) = %q, want %q", c.in, c.max, got, c.want)
		}
	}
}

func TestClean(t *testing.T) {
	if got := Clean("a\nb\u200fc"); got != "a b c" {
		t.Errorf("%q", got)
	}
}

func TestErr(t *testing.T) {
	if got := Err(errors.New("a\nb")); got != "a b" {
		t.Errorf("%q", got)
	}
}
