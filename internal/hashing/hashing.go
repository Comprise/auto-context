// Package hashing computes unit source hashes with an mtime/size cache.
package hashing

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"slices"
	"sync"
	"time"

	"auto-context/internal/fsutil"
)

// Entry caches the content hash of one file with the stat data it was taken at.
// Racy marks a sum taken less than racyMargin after the mtime: a same-size
// rewrite in the same mtime tick would leave mtime and size as they are (git's
// "racily clean"), so the entry serves only the process that hashed it.
type Entry struct {
	MtimeNs int64  `json:"mtime_ns"`
	Size    int64  `json:"size"`
	Sum     string `json:"sum"`
	Racy    bool   `json:"racy,omitempty"`
}

// racyMargin covers coarse mtime granularity (2 s on FAT, 1 s on ext3/HFS+)
// and small clock skew between the file system and us.
const racyMargin = 2 * time.Second

// Cache maps root-relative slash paths to entries. Its fields are private so
// that every change marks it dirty.
type Cache struct {
	entries map[string]Entry
	dirty   bool // changed since load: Save has something to write
	// stat, if set, gives the mtime and size Discover already read, saving
	// an Lstat per cached file.
	stat func(rel string) (mtimeNs, size int64, ok bool)
}

// cacheFile is the on-disk form of a Cache.
type cacheFile struct {
	Version int              `json:"version"`
	Entries map[string]Entry `json:"entries"`
}

// cacheVersion names the meaning of cached sums (content with CRLF as LF) and
// entries (3: racy entries are marked): a cache file of another version is
// discarded.
const cacheVersion = 3

func cachePath(root string) string {
	return fsutil.StatePath(root, "cache.json")
}

// NewCache returns an empty cache.
func NewCache() *Cache { return &Cache{entries: map[string]Entry{}} }

// LoadCache returns the stored cache, or an empty one, without racy entries
// (their files are hashed again). A missing file is not an error; one that cannot be used (too large, not a regular file, corrupt)
// gives an empty cache marked for rewriting and the error, so callers can say
// why hashing is cold. A cache of another version is replaced silently.
func LoadCache(root string) (*Cache, error) {
	empty := NewCache()
	data, err := fsutil.ReadFile(root, cachePath(root), maxCache)
	if errors.Is(err, os.ErrNotExist) {
		return empty, nil
	}
	empty.dirty = true
	if err != nil {
		return empty, fmt.Errorf("hash cache unusable, rehashing everything: %w", err)
	}
	var f cacheFile
	if err := json.Unmarshal(data, &f); err != nil {
		return empty, fmt.Errorf("hash cache corrupt, rehashing everything: %w", err)
	}
	if f.Version != cacheVersion || f.Entries == nil {
		return empty, nil
	}
	c := &Cache{entries: f.Entries}
	for rel, e := range c.entries {
		if e.Racy {
			delete(c.entries, rel)
			c.dirty = true
		}
	}
	return c, nil
}

// maxCache caps the size of the cache file we read (about 1.8M entries).
const maxCache = 256 << 20

// UseStats makes the cache trust stat for mtime and size (the values Discover
// read) instead of an Lstat per file. stat must come from the Tree whose
// files are hashed in this process: older stats would hide later edits.
func (c *Cache) UseStats(stat func(rel string) (mtimeNs, size int64, ok bool)) { c.stat = stat }

// Len is the number of entries.
func (c *Cache) Len() int { return len(c.entries) }

// Prune drops entries of files not in tracked, so deleted files do not pile up.
func (c *Cache) Prune(tracked []string) {
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
}

// Save writes the cache atomically (without fsync: it can be rebuilt), and
// only if it changed or does not exist yet.
func (c *Cache) Save(root string) error {
	if !c.dirty {
		if _, err := os.Lstat(cachePath(root)); err == nil {
			return nil
		}
	}
	f := cacheFile{Version: cacheVersion, Entries: c.entries}
	if f.Entries == nil {
		f.Entries = map[string]Entry{}
	}
	data, err := json.Marshal(f)
	if err != nil {
		return err
	}
	if err := fsutil.WriteState(root, cachePath(root), data); err != nil {
		return err
	}
	c.dirty = false
	return nil
}

// SourceHash returns "sha256:" + hex(sha256(for each sorted file: relpath +
// "\x00" + hex(sha256(content with CRLF as LF)) + "\n", then for each sorted
// extra: "\x00unit\x00" + extra + "\n")). Line endings are normalized so a
// Windows checkout (autocrlf) hashes like any other. Files are re-read only
// when mtime or size differ from the cache, which is updated.
func SourceHash(root string, files, extra []string, c *Cache) (string, error) {
	if c.entries == nil {
		c.entries = map[string]Entry{}
	}
	sorted := append([]string(nil), files...)
	slices.Sort(sorted)
	h := sha256.New()
	for _, f := range sorted {
		sum, err := fileSum(root, f, c)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(h, "%s\x00%s\n", f, sum)
	}
	ex := append([]string(nil), extra...)
	slices.Sort(ex)
	for _, e := range ex {
		fmt.Fprintf(h, "\x00unit\x00%s\n", e)
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

func fileSum(root, rel string, c *Cache) (string, error) {
	if c.stat != nil {
		if mt, size, ok := c.stat(rel); ok {
			if e, ok := c.entries[rel]; ok && e.MtimeNs == mt && e.Size == size {
				return e.Sum, nil
			}
		}
	}
	if e, ok := c.entries[rel]; ok {
		// Without trusted stats, compare with the file as it is now.
		if fi, err := os.Lstat(fsutil.Abs(root, rel)); err == nil && fi.Mode().IsRegular() &&
			e.MtimeNs == fi.ModTime().UnixNano() && e.Size == fi.Size() {
			return e.Sum, nil
		}
	}
	e, err := hashFile(root, rel, nil)
	if err != nil {
		return "", err
	}
	c.entries[rel] = e
	c.dirty = true
	return e.Sum, nil
}

// hashFile hashes one regular file (CRLF as LF) and returns its entry: the sum
// with the mtime and size it had, racy if the mtime is not racyMargin older
// than the read. buf, if set, is the copy buffer.
func hashFile(root, rel string, buf []byte) (Entry, error) {
	path := fsutil.Abs(root, rel)
	// Before the Lstat: a write after it gets a later mtime or the same one.
	start := time.Now()
	// Lstat: Discover never tracks symlinked sources, and one appearing since must not
	// make us read outside the repo (or block on a FIFO).
	fi, err := os.Lstat(path)
	if err != nil {
		return Entry{}, err
	}
	if !fi.Mode().IsRegular() {
		return Entry{}, fmt.Errorf("%s: %w", rel, fsutil.ErrNotRegular)
	}
	f, err := os.Open(path)
	if err != nil {
		return Entry{}, err
	}
	defer f.Close()
	h := sha256.New()
	lf := &lfWriter{w: h}
	if buf == nil {
		buf = make([]byte, 32<<10)
	}
	if _, err := io.CopyBuffer(lf, f, buf); err != nil {
		return Entry{}, err
	}
	if err := lf.flush(); err != nil {
		return Entry{}, err
	}
	return Entry{
		MtimeNs: fi.ModTime().UnixNano(),
		Size:    fi.Size(),
		Sum:     hex.EncodeToString(h.Sum(nil)),
		Racy:    fi.ModTime().After(start.Add(-racyMargin)),
	}, nil
}

// Prewarm hashes, on several goroutines, the files among rels whose entry is
// missing or out of date (per the trusted stats), so later SourceHash calls
// hit the cache. save, if set, is called every saveEvery while results come
// in, so a hook killed by its timeout keeps the work done. Errors are left for
// SourceHash to report.
func (c *Cache) Prewarm(root string, rels []string, save func(), saveEvery time.Duration) {
	if c.stat == nil {
		return
	}
	if c.entries == nil {
		c.entries = map[string]Entry{}
	}
	var todo []string
	for _, rel := range rels {
		mt, size, ok := c.stat(rel)
		if e, hit := c.entries[rel]; !ok || (hit && e.MtimeNs == mt && e.Size == size) {
			continue
		}
		todo = append(todo, rel)
	}
	if len(todo) == 0 {
		return
	}
	type result struct {
		rel string
		e   Entry
		ok  bool
	}
	jobs := make(chan string)
	results := make(chan result)
	workers := min(runtime.GOMAXPROCS(0), 8, len(todo))
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			buf := make([]byte, 32<<10)
			for rel := range jobs {
				e, err := hashFile(root, rel, buf)
				results <- result{rel, e, err == nil}
			}
		}()
	}
	go func() {
		for _, rel := range todo {
			jobs <- rel
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()
	last := time.Now()
	for r := range results {
		if r.ok {
			c.entries[r.rel] = r.e
			c.dirty = true
		}
		if save != nil && time.Since(last) > saveEvery {
			save()
			last = time.Now()
		}
	}
}

// lfWriter passes bytes through with every "\r\n" written as "\n".
type lfWriter struct {
	w  io.Writer
	cr bool // the previous chunk ended with '\r'

}

func (l *lfWriter) Write(p []byte) (int, error) {
	n := len(p)
	if l.cr {
		l.cr = false
		if len(p) == 0 || p[0] != '\n' {
			if _, err := l.w.Write([]byte{'\r'}); err != nil {
				return 0, err
			}
		}
	}
	// Fast path: write the runs between '\r' bytes as they are.
	for len(p) > 0 {
		i := bytes.IndexByte(p, '\r')
		if i < 0 {
			if _, err := l.w.Write(p); err != nil {
				return 0, err
			}
			break
		}
		if _, err := l.w.Write(p[:i]); err != nil {
			return 0, err
		}
		p = p[i+1:]
		if len(p) == 0 {
			l.cr = true // decided by the next chunk
			break
		}
		if p[0] != '\n' {
			if _, err := l.w.Write([]byte{'\r'}); err != nil {
				return 0, err
			}
		}
	}
	return n, nil
}

func (l *lfWriter) flush() error {
	if !l.cr {
		return nil
	}
	l.cr = false
	_, err := l.w.Write([]byte{'\r'})
	return err
}
