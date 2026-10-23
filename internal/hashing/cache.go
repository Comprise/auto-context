package hashing

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"time"

	"auto-context/internal/filelock"
	"auto-context/internal/fsutil"
)

// Entry caches the content hash of one file with the stat data it was taken at.
// Racy marks a sum taken less than racyMargin after the mtime: a same-size
// rewrite in the same mtime tick would leave mtime and size as they are (git's
// "racily clean"), so the entry serves only the process that hashed it.
type Entry struct {
	MtimeNs int64
	Size    int64
	Sum     [sha256Size]byte
	Racy    bool
}

const sha256Size = 32

// racyMargin covers coarse mtime granularity (2 s on FAT, 1 s on ext3/HFS+)
// and small clock skew between the file system and us.
const racyMargin = 2 * time.Second

// Cache maps root-relative slash paths to entries. Its fields are private so
// that every change marks it dirty.
type Cache struct {
	entries map[string]Entry
	dirty   bool // changed since load: Save has something to write
	// tracked is the file set of the last Prune since the last save: Save
	// does not take entries of other files from the file on disk.
	tracked map[string]bool
	// stat, if set, gives the mtime and size Discover already read, saving
	// an Lstat per cached file.
	stat func(rel string) (mtimeNs, size int64, ok bool)
}

// The cache file (version cacheVersion) is cacheHeader, then one record per
// entry, sorted by path:
//
//	uvarint(len(path)) path | varint(mtime_ns) | uvarint(size) | [32]byte sum | flags
//
// flags bit 0 marks a racy entry; other bits are zero. Each version has its
// own file, so plugin versions sharing a tree (Claude and Codex installs) do
// not evict each other's caches; files of other versions are neither read
// nor touched.
var cacheHeader = fmt.Sprintf("acache %d\n", cacheVersion)

const flagRacy = 1

// CachePath is the cache file of this version under root.
func CachePath(root string) string {
	return fsutil.StatePath(root, fmt.Sprintf("cache.v%d.bin", cacheVersion))
}

// NewCache returns an empty cache.
func NewCache() *Cache { return &Cache{entries: map[string]Entry{}} }

// LoadCache returns the stored cache, or an empty one, without racy entries
// (their files are hashed again). A missing file is not an error; one that
// cannot be used (too large, not a regular file, corrupt) gives an empty
// cache marked for rewriting and the error, so callers can say why hashing
// is cold.
func LoadCache(root string) (*Cache, error) {
	empty := NewCache()
	data, err := fsutil.ReadFile(root, CachePath(root), maxCache)
	if errors.Is(err, os.ErrNotExist) {
		return empty, nil
	}
	empty.dirty = true
	if err != nil {
		return empty, fmt.Errorf("hash cache unusable, rehashing everything: %w", err)
	}
	// About 70 bytes per record with repository-like paths.
	entries := make(map[string]Entry, len(data)/70)
	err = walkCache(data, func(path string, e Entry) {
		// Not dirty for a racy entry left out: dropping one is not a change
		// worth a rewrite; it is replaced once its file is hashed again.
		if !e.Racy {
			entries[path] = e
		}
	})
	if err != nil {
		return empty, fmt.Errorf("hash cache corrupt, rehashing everything: %w", err)
	}
	return &Cache{entries: entries}, nil
}

// maxCache caps the size of the cache file we read (millions of entries).
const maxCache = 256 << 20

// maxCachePath caps the path length in a record: a longer one means a
// corrupt file, not a path.
const maxCachePath = 64 << 10

var errCacheFormat = errors.New("bad cache record")

// localPath reports whether p is a clean root-relative slash path that stays
// in the root: not empty or absolute, no empty, "." or ".." segment, no NUL
// or backslash; on Windows also filepath.IsLocal (reserved names, volumes).
// One pass over p: it runs for every record of a large cache.
func localPath(p string) bool {
	if p == "" {
		return false
	}
	start := 0
	for i := 0; i <= len(p); i++ {
		if i < len(p) {
			if c := p[i]; c == 0 || c == '\\' {
				return false
			} else if c != '/' {
				continue
			}
		}
		if seg := p[start:i]; seg == "" || seg == "." || seg == ".." {
			return false
		}
		start = i + 1
	}
	return runtime.GOOS != "windows" || filepath.IsLocal(filepath.FromSlash(p))
}

// walkCache parses a cache file, calling fn for each record in order. On an
// error fn has seen only the records before it. The file may come from a
// hostile repo (a committed .auto-context): a path must be a clean, local,
// slash-separated relative path, so an entry can never name a file outside
// the repo.
func walkCache(data []byte, fn func(path string, e Entry)) error {
	if !bytes.HasPrefix(data, []byte(cacheHeader)) {
		return errors.New("bad cache header")
	}
	b := data[len(cacheHeader):]
	var prev []byte
	for len(b) > 0 {
		n, k := binary.Uvarint(b)
		if k <= 0 || n == 0 || n > maxCachePath || n > uint64(len(b)-k) {
			return errCacheFormat
		}
		raw := b[k : k+int(n)]
		if prev != nil && bytes.Compare(prev, raw) >= 0 {
			return errCacheFormat // unsorted or duplicate
		}
		prev = raw
		rel := string(raw)
		if !localPath(rel) {
			return errCacheFormat
		}
		b = b[k+int(n):]
		var e Entry
		if e.MtimeNs, k = binary.Varint(b); k <= 0 {
			return errCacheFormat
		}
		b = b[k:]
		size, k := binary.Uvarint(b)
		if k <= 0 || size > 1<<62 {
			return errCacheFormat
		}
		e.Size = int64(size)
		b = b[k:]
		if len(b) < sha256Size+1 || b[sha256Size]&^flagRacy != 0 {
			return errCacheFormat
		}
		copy(e.Sum[:], b)
		e.Racy = b[sha256Size]&flagRacy != 0
		b = b[sha256Size+1:]
		fn(rel, e)
	}
	return nil
}

// encodeCache returns the file form of entries.
func encodeCache(entries map[string]Entry) []byte {
	keys := slices.Sorted(maps.Keys(entries))
	if len(keys) > 0 && keys[0] == "" {
		keys = keys[1:] // not a file; the format has no empty path
	}
	n := len(cacheHeader)
	for _, k := range keys {
		n += len(k) + 2*binary.MaxVarintLen64 + sha256Size + 2
	}
	out := make([]byte, 0, n)
	out = append(out, cacheHeader...)
	for _, k := range keys {
		e := entries[k]
		out = binary.AppendUvarint(out, uint64(len(k)))
		out = append(out, k...)
		out = binary.AppendVarint(out, e.MtimeNs)
		out = binary.AppendUvarint(out, uint64(e.Size))
		out = append(out, e.Sum[:]...)
		var flags byte
		if e.Racy {
			flags |= flagRacy
		}
		out = append(out, flags)
	}
	return out
}

// UseStats makes the cache trust stat for mtime and size (the values Discover
// read) instead of an Lstat per file. stat must come from the Tree whose
// files are hashed in this process: older stats would hide later edits.
func (c *Cache) UseStats(stat func(rel string) (mtimeNs, size int64, ok bool)) { c.stat = stat }

// Len is the number of entries.
func (c *Cache) Len() int { return len(c.entries) }

// Dirty reports whether the cache changed since it was loaded or saved.
func (c *Cache) Dirty() bool { return c.dirty }

// Prune drops entries of files not in tracked, so deleted files do not pile up.
func (c *Cache) Prune(tracked []string) {
	if len(c.entries) == 0 {
		return
	}
	keep := make(map[string]bool, len(tracked))
	for _, f := range tracked {
		keep[f] = true
	}
	for k := range c.entries {
		if !keep[k] {
			delete(c.entries, k)
			c.dirty = true
		}
	}
	c.tracked = keep
}

// Save writes the cache atomically (without fsync: it can be rebuilt), and
// only if it changed or does not exist yet. Under a lock on the file it
// merges the entries other processes (parallel sessions) saved meanwhile,
// so their work is not lost: an entry on disk is kept when it matches the
// file's current mtime and size and ours does not. A lock not taken within
// filelock.Wait still merges, only without the guard.
func (c *Cache) Save(root string) error {
	path := CachePath(root)
	if !c.dirty {
		if _, err := os.Lstat(path); err == nil {
			return nil
		}
	}
	unlock := lockCache(root, path)
	defer unlock()
	if err := fsutil.WriteState(root, path, encodeCache(c.merged(root, path))); err != nil {
		return err
	}
	c.dirty = false
	c.tracked = nil
	return nil
}

// lockCache takes the lock file next to the cache, best effort: without it
// two saves may still overwrite each other, which loses work but no data.
func lockCache(root, path string) (unlock func()) {
	noop := func() {}
	lp := path + ".lock"
	if _, err := fsutil.Resolve(root, lp); err != nil {
		return noop
	}
	if err := os.MkdirAll(filepath.Dir(lp), 0o755); err != nil {
		return noop
	}
	// The lexical path: filelock refuses a symlink or FIFO there.
	unlock, ok, err := filelock.Lock(lp)
	if err != nil || !ok {
		return noop
	}
	return unlock
}

// merged returns our entries with those of the file on disk merged in (see
// Save). An unreadable file merges nothing. Racy entries on disk served only
// their process, and entries of files the last Prune did not track are
// stale: both are left out.
func (c *Cache) merged(root, path string) map[string]Entry {
	data, err := fsutil.ReadFile(root, path, maxCache)
	if err != nil {
		return c.entries
	}
	type taken struct {
		rel string
		e   Entry
	}
	var take []taken
	err = walkCache(data, func(rel string, d Entry) {
		mine, have := c.entries[rel]
		if (have && mine == d) || d.Racy {
			return
		}
		if c.tracked != nil && !c.tracked[rel] {
			return
		}
		mt, size, ok := c.statOf(root, rel)
		if !ok || d.MtimeNs != mt || d.Size != size {
			return
		}
		if have && mine.MtimeNs == mt && mine.Size == size {
			return // both current: ours stays
		}
		take = append(take, taken{rel, d})
	})
	if err != nil || len(take) == 0 {
		return c.entries
	}
	out := maps.Clone(c.entries)
	if out == nil {
		out = make(map[string]Entry, len(take))
	}
	for _, t := range take {
		out[t.rel] = t.e
	}
	return out
}

// statOf returns the mtime and size of rel: the trusted stats if set (a
// file not in the tree is not ok), else an Lstat of a regular file.
func (c *Cache) statOf(root, rel string) (mtimeNs, size int64, ok bool) {
	if c.stat != nil {
		return c.stat(rel)
	}
	fi, err := os.Lstat(fsutil.Abs(root, rel))
	if err != nil || !fi.Mode().IsRegular() {
		return 0, 0, false
	}
	return fi.ModTime().UnixNano(), fi.Size(), true
}
