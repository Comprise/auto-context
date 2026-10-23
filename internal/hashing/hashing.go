// Package hashing computes unit source hashes with an mtime/size cache.
package hashing

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"runtime"
	"slices"
	"sync"
	"time"

	"auto-context/internal/fsutil"
)

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
	var line [2*sha256Size + 2]byte
	line[len(line)-1] = '\n'
	for _, f := range sorted {
		sum, err := fileSum(ctx, root, f, c)
		if err != nil {
			return "", err
		}
		hex.Encode(line[1:len(line)-1], sum[:])
		io.WriteString(h, f)
		h.Write(line[:])
	}
	ex := append([]string(nil), extra...)
	slices.Sort(ex)
	for _, e := range ex {
		fmt.Fprintf(h, "\x00unit\x00%s\n", e)
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

func fileSum(ctx context.Context, root, rel string, c *Cache) ([sha256Size]byte, error) {
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
		return [sha256Size]byte{}, err
	}
	e, err := hashFile(ctx, root, rel, nil)
	if err != nil {
		return [sha256Size]byte{}, err
	}
	c.entries[rel] = e
	c.dirty = true
	return e.Sum, nil
}

// cacheVersion names the meaning of cached sums (content with CRLF as LF) and
// entries (3: racy entries are marked; 4: files over MaxHashSize are sampled;
// 5: the binary file of cache.go). cmd/auto-context prints it in version -v.
const cacheVersion = 5

// MaxHashSize is the file size above which only a sample is hashed.
const MaxHashSize = 32 << 20

// sampleSize is the length of the head and of the tail hashed for a file
// larger than MaxHashSize.
const sampleSize = 1 << 20

// largeSum is the sum of a file larger than MaxHashSize: sha256("large\x00" +
// decimal size + "\x00" + LF(first 1 MiB) + LF(last 1 MiB)). A change in the
// middle that keeps size and mtime goes unseen (documented limitation).
func largeSum(r io.ReaderAt, size int64) ([sha256Size]byte, error) {
	h := sha256.New()
	fmt.Fprintf(h, "large\x00%d\x00", size)
	for _, off := range []int64{0, size - sampleSize} {
		lf := &lfWriter{w: h}
		if _, err := io.Copy(lf, io.NewSectionReader(r, off, sampleSize)); err != nil {
			return [sha256Size]byte{}, err
		}
		if err := lf.flush(); err != nil {
			return [sha256Size]byte{}, err
		}
	}
	return [sha256Size]byte(h.Sum(nil)), nil
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
	var sum [sha256Size]byte
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
		sum = [sha256Size]byte(h.Sum(nil))
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
