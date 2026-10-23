// Package hashing computes unit source hashes with an mtime/size cache.
package hashing

import (
	"bytes"
	"context"
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
// entries (3: racy entries are marked; 4: files over MaxHashSize are sampled):
// a cache file of another version is discarded.
const cacheVersion = 4

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
			// Not dirty: dropping a racy entry is not a change worth a
			// rewrite; the entry is replaced once its file is hashed again.
			delete(c.entries, rel)
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
// "\x00" + hex(sha256(content with CRLF as LF)) (for a file over MaxHashSize
// the sampled sum of largeSum) + "\n", then for each sorted
// extra: "\x00unit\x00" + extra + "\n")). Line endings are normalized so a
// Windows checkout (autocrlf) hashes like any other. Files are re-read only
// when mtime or size differ from the cache, which is updated. Once ctx has
// ended it stops with ctx's error at the first file the cache does not
// answer for; the sums taken so far stay in the cache.
func SourceHash(ctx context.Context, root string, files, extra []string, c *Cache) (string, error) {
	if c.entries == nil {
		c.entries = map[string]Entry{}
	}
	sorted := append([]string(nil), files...)
	slices.Sort(sorted)
	h := sha256.New()
	for _, f := range sorted {
		sum, err := fileSum(ctx, root, f, c)
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

func fileSum(ctx context.Context, root, rel string, c *Cache) (string, error) {
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
	if err := ctx.Err(); err != nil {
		return "", err
	}
	e, err := hashFile(ctx, root, rel, nil)
	if err != nil {
		return "", err
	}
	c.entries[rel] = e
	c.dirty = true
	return e.Sum, nil
}

// MaxHashSize is the file size above which only a sample is hashed.
const MaxHashSize = 32 << 20

// sampleSize is the length of the head and of the tail hashed for a file
// larger than MaxHashSize.
const sampleSize = 1 << 20

// largeSum is the sum of a file larger than MaxHashSize: sha256("large\x00" +
// decimal size + "\x00" + LF(first 1 MiB) + LF(last 1 MiB)). A change in the
// middle that keeps size and mtime goes unseen (documented limitation).
func largeSum(r io.ReaderAt, size int64) (string, error) {
	h := sha256.New()
	fmt.Fprintf(h, "large\x00%d\x00", size)
	for _, off := range []int64{0, size - sampleSize} {
		lf := &lfWriter{w: h}
		if _, err := io.Copy(lf, io.NewSectionReader(r, off, sampleSize)); err != nil {
			return "", err
		}
		if err := lf.flush(); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// hashFile hashes one regular file (CRLF as LF) and returns its entry: the sum
// with the mtime and size it had, racy if the mtime is not racyMargin older
// than the read. buf, if set, is the copy buffer. Once ctx has ended the read
// stops with ctx's error.
func hashFile(ctx context.Context, root, rel string, buf []byte) (Entry, error) {
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
	var sum string
	if fi.Size() > MaxHashSize {
		if sum, err = largeSum(ctxReaderAt{ctx, f}, fi.Size()); err != nil {
			return Entry{}, err
		}
	} else {
		h := sha256.New()
		lf := &lfWriter{w: h}
		if buf == nil {
			buf = make([]byte, 32<<10)
		}
		if _, err := io.CopyBuffer(lf, ctxReader{ctx, f}, buf); err != nil {
			return Entry{}, err
		}
		if err := lf.flush(); err != nil {
			return Entry{}, err
		}
		sum = hex.EncodeToString(h.Sum(nil))
	}
	return Entry{
		MtimeNs: fi.ModTime().UnixNano(),
		Size:    fi.Size(),
		Sum:     sum,
		Racy:    fi.ModTime().After(start.Add(-racyMargin)),
	}, nil
}

// ctxReader reads r until ctx ends, then fails with ctx's error: a large
// file must not hold a hook past its budget.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

// ctxReaderAt is ctxReader for io.ReaderAt.
type ctxReaderAt struct {
	ctx context.Context
	r   io.ReaderAt
}

func (c ctxReaderAt) ReadAt(p []byte, off int64) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.ReadAt(p, off)
}

// Prewarm hashes, on several goroutines, the files among rels whose entry is
// missing or out of date (per the trusted stats), so later SourceHash calls
// hit the cache. save, if set, is called every saveEvery while results come
// in, so a hook killed by its timeout keeps the work done. Once ctx has ended
// no new file is started and Prewarm returns with what it has. Errors are
// left for SourceHash to report.
func (c *Cache) Prewarm(ctx context.Context, root string, rels []string, save func(), saveEvery time.Duration) {
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
	results := make(chan result, 256)
	workers := min(runtime.GOMAXPROCS(0), 8, len(todo))
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			buf := make([]byte, 32<<10)
			for rel := range jobs {
				e, err := hashFile(ctx, root, rel, buf)
				results <- result{rel, e, err == nil}
			}
		})
	}
	go func() {
	feed:
		for _, rel := range todo {
			select {
			case jobs <- rel:
			case <-ctx.Done():
				break feed
			}
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()
	apply := func(r result) {
		if r.ok {
			c.entries[r.rel] = r.e
			c.dirty = true
		}
	}
	// save runs in its own goroutine so a slow write does not hold up the
	// workers. While it runs the cache is only read, so results wait in
	// pending and are applied after it.
	var pending []result
	var saved chan struct{} // non-nil while save runs
	endSave := func() {
		<-saved
		saved = nil
		for _, r := range pending {
			apply(r)
		}
		pending = nil
	}
	last := time.Now()
	for {
		select {
		case r, open := <-results:
			if !open {
				if saved != nil {
					endSave()
				}
				return
			}
			if saved != nil {
				pending = append(pending, r)
				continue
			}
			apply(r)
			if save != nil && time.Since(last) > saveEvery {
				saved = make(chan struct{})
				go func() {
					defer close(saved)
					save()
				}()
			}
		case <-saved:
			endSave()
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
