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

func TestStripControls(t *testing.T) {
	cases := []struct{ in, want string }{
		{"abc 1 файл\n\tx | 2 +-\n", "abc 1 файл\n\tx | 2 +-\n"},
		{"a\x1b[31mred\x1b[0m\n", "a[31mred[0m\n"},
		{"t\x1b]0;title\x07x\r\n", "t]0;titlex\n"},
		{"c1\u009b31m\u0085 del\x7f\n", "c131m del\n"},
		{"raw\x9b31m \xff\xfe\n", "raw31m \xff\xfe\n"},
		{"", ""},
	}
	for _, c := range cases {
		if got := string(StripControls([]byte(c.in))); got != c.want {
			t.Errorf("StripControls(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	clean := []byte("plain\n")
	if got := StripControls(clean); &got[0] != &clean[0] {
		t.Error("clean input was copied")
	}
}
