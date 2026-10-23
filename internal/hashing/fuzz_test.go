package hashing

import (
	"strings"
	"testing"
)

// FuzzLFWriter: whatever the chunking, the output is the input with "\r\n"
// written as "\n" (a lone CR at a chunk boundary included).
func FuzzLFWriter(f *testing.F) {
	f.Add("a\r\nb\rc\r", uint8(1))
	f.Add("\r\r\n\n\r", uint8(2))
	f.Add("", uint8(0))
	f.Fuzz(func(t *testing.T, s string, chunk uint8) {
		var out strings.Builder
		w := &lfWriter{w: &out}
		n := int(chunk%16) + 1
		for rest := []byte(s); len(rest) > 0; {
			k := min(n, len(rest))
			if _, err := w.Write(rest[:k]); err != nil {
				t.Fatal(err)
			}
			rest = rest[k:]
		}
		if err := w.flush(); err != nil {
			t.Fatal(err)
		}
		if want := strings.ReplaceAll(s, "\r\n", "\n"); out.String() != want {
			t.Errorf("chunk %d: got %q, want %q", n, out.String(), want)
		}
	})
}
