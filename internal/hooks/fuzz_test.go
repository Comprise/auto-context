package hooks

import (
	"slices"
	"strings"
	"testing"
)

// FuzzAllowedCommand pins what an approved command may look like: plain
// space-separated words of [A-Za-z0-9_./-] that the shell splits exactly like
// strings.Split, with no ".." and no "--exclude" anywhere.
func FuzzAllowedCommand(f *testing.F) {
	for _, c := range allowedCLICalls {
		f.Add(c)
	}
	for _, c := range []string{
		"auto-context ack --exclude x",
		"auto-context ack --excluded",
		"auto-context ack a/../b",
		"auto-context ack svc; ls",
		"auto-context  ack svc",
		"auto-context ack x ",
		"auto-context status\n",
	} {
		f.Add(c)
	}
	f.Fuzz(func(t *testing.T, cmd string) {
		if !allowedCommand(cmd) {
			return
		}
		if !slices.Equal(strings.Fields(cmd), strings.Split(cmd, " ")) {
			t.Errorf("%q: Fields and Split differ", cmd)
		}
		if strings.Contains(cmd, "..") || strings.Contains(cmd, "--exclude") {
			t.Errorf("%q: approved with .. or --exclude", cmd)
		}
		for i := 0; i < len(cmd); i++ {
			b := cmd[i]
			if !(b >= 'A' && b <= 'Z' || b >= 'a' && b <= 'z' || b >= '0' && b <= '9' || strings.IndexByte("_./ -", b) >= 0) {
				t.Errorf("%q: approved with byte %#x", cmd, b)
			}
		}
	})
}
