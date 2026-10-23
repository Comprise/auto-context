package hashing

import (
	"maps"
	"path/filepath"
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

// FuzzWalkCache: any file content parses or fails without panicking, and what
// parses encodes to a file that parses to the same entries.
func FuzzWalkCache(f *testing.F) {
	c := &Cache{entries: map[string]Entry{
		"a.go":   {MtimeNs: -5, Size: 3, Sum: [32]byte{1}},
		"b/c.go": {MtimeNs: 1 << 60, Size: 0, Racy: true},
	}}
	f.Add(encodeCache(c.entries))
	f.Add([]byte(cacheHeader))
	f.Add([]byte("acache 5\n\x01"))
	f.Fuzz(func(t *testing.T, data []byte) {
		entries := map[string]Entry{}
		if walkCache(data, func(p string, e Entry) { entries[p] = e }) != nil {
			return
		}
		for p := range entries {
			if !filepath.IsLocal(filepath.FromSlash(p)) || strings.ContainsAny(p, "\x00\\") {
				t.Errorf("non-local path accepted: %q", p)
			}
		}
		out := encodeCache(entries)
		again := map[string]Entry{}
		if err := walkCache(out, func(p string, e Entry) { again[p] = e }); err != nil || !maps.Equal(again, entries) {
			t.Errorf("re-encoded %q: %v %v, want %v", out, err, again, entries)
		}
	})
}
