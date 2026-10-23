// Package filestat holds the change-detection fingerprint of a file, shared
// by discovery, the session snapshot and the hooks without tying them together.
package filestat

import "io/fs"

// Stat is the change-detection fingerprint of one tracked file: its mtime in
// nanoseconds and its size.
type Stat struct {
	MtimeNs int64
	Size    int64
}

// Of returns the fingerprint of fi.
func Of(fi fs.FileInfo) Stat {
	return Stat{MtimeNs: fi.ModTime().UnixNano(), Size: fi.Size()}
}
