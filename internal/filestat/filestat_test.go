package filestat

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestOf(t *testing.T) {
	p := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(p, []byte("abc"), 0o644); err != nil {
		t.Fatal(err)
	}
	mt := time.Unix(1_700_000_000, 123_456_789)
	if err := os.Chtimes(p, mt, mt); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := Of(fi), (Stat{MtimeNs: fi.ModTime().UnixNano(), Size: 3}); got != want {
		t.Errorf("Of = %+v, want %+v", got, want)
	}
}
