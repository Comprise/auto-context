// Package session keeps the per-session files of a turn: the snapshot of
// tracked files taken at its start and the journal of what Stop saw. Both live
// in .auto-context/state/<session id>.{snapshot,json}.
package session

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"auto-context/internal/filestat"
	"auto-context/internal/fsutil"
)

// Entry is one file of a snapshot: its root-relative slash path and stat data.
type Entry struct {
	Path string
	Stat filestat.Stat
}

// Snapshot is the stat data of the tracked files, sorted by path, each path
// once. Kept as a sorted slice rather than a map: on large trees a second map
// of every file is a large share of a hook's memory, and Diff merges two
// sorted snapshots in one pass. Build one with FromTree, FromMap or
// LoadSnapshot.
type Snapshot []Entry

// FromTree builds a snapshot from the stat data Discover already collected,
// so a hook does not stat every tracked file twice. tracked is the tree's
// sorted file list (Tree.Tracked) and stats its stat map (Tree.StatsView),
// which is only read; a file without stat data is left out.
func FromTree(tracked []string, stats map[string]filestat.Stat) Snapshot {
	s := make(Snapshot, 0, len(stats))
	for _, p := range tracked {
		if st, ok := stats[p]; ok {
			s = append(s, Entry{p, st})
		}
	}
	return s.normalized()
}

// FromMap builds a snapshot from a path -> stat map.
func FromMap(m map[string]filestat.Stat) Snapshot {
	s := make(Snapshot, 0, len(m))
	for p, st := range m {
		s = append(s, Entry{p, st})
	}
	return s.normalized()
}

// normalized sorts s by path if it is not sorted yet and keeps the last
// entry of a path given more than once (as a map would).
func (s Snapshot) normalized() Snapshot {
	if s.isNormalized() {
		return s
	}
	slices.SortStableFunc(s, func(a, b Entry) int { return strings.Compare(a.Path, b.Path) })
	out := s[:0]
	for i, e := range s {
		if i+1 < len(s) && s[i+1].Path == e.Path {
			continue // a later entry of the same path wins
		}
		out = append(out, e)
	}
	clear(s[len(out):])
	return out
}

// sortedCopy returns s when it is normalized, else a normalized copy (s is
// not changed). The check is one pass over s.
func (s Snapshot) sortedCopy() Snapshot {
	if s.isNormalized() {
		return s
	}
	return slices.Clone(s).normalized()
}

// isNormalized reports whether s is sorted by path with each path once.
func (s Snapshot) isNormalized() bool {
	for i := 1; i < len(s); i++ {
		if s[i-1].Path >= s[i].Path {
			return false
		}
	}
	return true
}

// Get returns the stat data of path p, if s holds it. s must be normalized
// (sorted by path, each path once), as every constructor here leaves it.
func (s Snapshot) Get(p string) (filestat.Stat, bool) {
	i, ok := slices.BinarySearchFunc(s, p, func(e Entry, p string) int { return strings.Compare(e.Path, p) })
	if !ok {
		return filestat.Stat{}, false
	}
	return s[i].Stat, true
}

// Has reports whether s holds path p.
func (s Snapshot) Has(p string) bool {
	_, ok := s.Get(p)
	return ok
}

// Diff returns the sorted paths that changed, were removed or were added,
// merging the two snapshots in one pass. Both should be normalized (sorted by
// path, each path once), as every constructor here leaves them; one that is
// not (a hand-built slice) is normalized into a copy first.
func Diff(before, after Snapshot) []string {
	before, after = before.sortedCopy(), after.sortedCopy()
	var out []string
	i, j := 0, 0
	for i < len(before) && j < len(after) {
		b, a := before[i], after[j]
		switch c := strings.Compare(b.Path, a.Path); {
		case c < 0:
			out = append(out, b.Path) // removed
			i++
		case c > 0:
			out = append(out, a.Path) // added
			j++
		default:
			if a.Stat != b.Stat {
				out = append(out, a.Path)
			}
			i++
			j++
		}
	}
	for ; i < len(before); i++ {
		out = append(out, before[i].Path)
	}
	for ; j < len(after); j++ {
		out = append(out, after[j].Path)
	}
	return out
}

// SanitizeID maps a session id to a safe, short file name component. An id
// that needed changes (or is long) gets a hash suffix, so distinct ids in
// practice never share a file.
func SanitizeID(id string) string {
	b := []byte(id)
	changed := false
	for i, c := range b {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			b[i] = '_'
			changed = true
		}
	}
	if len(b) == 0 {
		return "_"
	}
	if !changed && len(b) <= 64 {
		return string(b)
	}
	if len(b) > 32 {
		b = b[:32]
	}
	sum := sha256.Sum256([]byte(id))
	return string(b) + "-" + hex.EncodeToString(sum[:8])
}

// ValidID reports whether id can name a session: it is not blank.
func ValidID(id string) bool { return strings.TrimSpace(id) != "" }

// MaxSize caps the size of a state file we read.
const MaxSize = 256 << 20

// statePath returns <root>/.auto-context/state/<sanitized id><ext>.
func statePath(root, sessionID, ext string) string {
	return fsutil.StatePath(root, "state", SanitizeID(sessionID)+ext)
}

// SnapshotPath is the snapshot file of a session.
func SnapshotPath(root, sessionID string) string {
	return statePath(root, sessionID, ".snapshot")
}

func journalPath(root, sessionID string) string {
	return statePath(root, sessionID, ".json")
}

// snapshotHeader starts a snapshot file; records follow as
// "<mtime_ns> <size> <path>\x00" (paths may contain spaces and newlines),
// sorted by path since sorted snapshots (older files of the same format are
// not, and are sorted on load).
const snapshotHeader = "auto-context snapshot 1\n"

// SnapshotTime returns the modification time (ns) of a session's snapshot
// file: when the turn started. A missing file yields an error wrapping
// os.ErrNotExist.
func SnapshotTime(root, sessionID string) (int64, error) {
	full, err := fsutil.Resolve(root, SnapshotPath(root, sessionID))
	if err != nil {
		return 0, err
	}
	fi, err := os.Stat(full)
	if err != nil {
		return 0, err
	}
	return fi.ModTime().UnixNano(), nil
}

// LoadSnapshot reads a session's snapshot; a missing file yields nil and
// an error matching os.ErrNotExist, and one without the header or with a bad
// record an error naming the file.
func LoadSnapshot(root, sessionID string) (Snapshot, error) {
	data, err := fsutil.ReadFile(root, SnapshotPath(root, sessionID), MaxSize)
	if err != nil {
		return nil, err
	}
	body, ok := bytes.CutPrefix(data, []byte(snapshotHeader))
	if !ok {
		return nil, fmt.Errorf("snapshot %s: no header", SnapshotPath(root, sessionID))
	}
	s := make(Snapshot, 0, bytes.Count(body, []byte{0}))
	for len(body) > 0 {
		rec, rest, found := bytes.Cut(body, []byte{0})
		if !found {
			return nil, fmt.Errorf("snapshot %s: truncated record", SnapshotPath(root, sessionID))
		}
		body = rest
		mtime, afterMtime, ok1 := bytes.Cut(rec, []byte{' '})
		size, path, ok2 := bytes.Cut(afterMtime, []byte{' '})
		if !ok1 || !ok2 {
			return nil, fmt.Errorf("snapshot %s: bad record", SnapshotPath(root, sessionID))
		}
		m, err1 := strconv.ParseInt(string(mtime), 10, 64)
		n, err2 := strconv.ParseInt(string(size), 10, 64)
		if err1 != nil || err2 != nil {
			return nil, fmt.Errorf("snapshot %s: bad number", SnapshotPath(root, sessionID))
		}
		s = append(s, Entry{string(path), filestat.Stat{MtimeNs: m, Size: n}})
	}
	// Written sorted; a file from before snapshots were sorted (same
	// format) is sorted here.
	return s.normalized(), nil
}

// snapshotMaxAge caps the age of a snapshot: a turn does not outlive a day, and
// snapshots are the largest state files.
const snapshotMaxAge = 24 * time.Hour

// RemoveStale deletes every regular file in the state dir not modified within
// maxAge (snapshots: within the lesser of maxAge and 24h): files of sessions
// that ended without SessionEnd, the session mark of an abandoned project,
// leftover temporary files.
func RemoveStale(root string, maxAge time.Duration, now time.Time) error {
	dir, err := fsutil.Resolve(root, fsutil.StatePath(root, "state"))
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	cutoff := now.Add(-maxAge)
	snapCutoff := now.Add(-min(maxAge, snapshotMaxAge))
	var errs []error
	for _, e := range entries {
		fi, err := e.Info()
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				errs = append(errs, err)
			}
			continue
		}
		c := cutoff
		if strings.HasSuffix(e.Name(), ".snapshot") {
			c = snapCutoff
		}
		if !fi.Mode().IsRegular() || fi.ModTime().After(c) {
			continue
		}
		if err := os.Remove(filepath.Join(dir, e.Name())); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Save writes the session's snapshot atomically, in path order.
func (s Snapshot) Save(root, sessionID string) error {
	n := len(snapshotHeader)
	for _, e := range s {
		n += len(e.Path) + 2*20 + 3 // two numbers, two spaces, the NUL
	}
	buf := make([]byte, 0, n)
	buf = append(buf, snapshotHeader...)
	for _, e := range s {
		buf = strconv.AppendInt(buf, e.Stat.MtimeNs, 10)
		buf = append(buf, ' ')
		buf = strconv.AppendInt(buf, e.Stat.Size, 10)
		buf = append(buf, ' ')
		buf = append(buf, e.Path...)
		buf = append(buf, 0)
	}
	return fsutil.WriteState(root, SnapshotPath(root, sessionID), buf)
}

// Journal records what happened during the current turn, across its Stop
// calls; PromptSubmit resets it.
type Journal struct {
	// Version is the file format; Save stamps journalVersion and LoadJournal
	// drops a journal of any other version.
	Version         int      `json:"version"`
	CodeChanged     []string `json:"code_changed"`
	DocsTouched     []string `json:"docs_touched"`
	AlreadyNotified []string `json:"already_notified"`
	// AckFailNotified: units whose failed auto-ack Stop already reported.
	AckFailNotified []string `json:"ack_fail_notified,omitempty"`
	// Paused: the config was broken during the session; a snapshot from
	// before the pause, if any, is kept until tracking resumes.
	// ConfigNotified: Stop said so.
	// Change them through Pause, MarkConfigNotified and Resume.
	Paused         bool `json:"paused,omitempty"`
	ConfigNotified bool `json:"config_notified,omitempty"`
}

// journalVersion is the current journal file format.
const journalVersion = 1

// Pause records that the config is broken.
func (j *Journal) Pause() { j.Paused = true }

// MarkConfigNotified pauses and reports whether the pause was not reported yet.
func (j *Journal) MarkConfigNotified() (first bool) {
	first = !j.ConfigNotified
	j.Paused, j.ConfigNotified = true, true
	return first
}

// Resume ends a pause: the flags are cleared, and so are notifications from
// before it, which must not hide the changes made meanwhile.
func (j *Journal) Resume() {
	j.Paused, j.ConfigNotified, j.AlreadyNotified, j.AckFailNotified = false, false, nil, nil
}

// NotifyAckFailed records units whose failed auto-ack was reported.
func (j *Journal) NotifyAckFailed(us ...string) {
	j.AckFailNotified = AddUnique(j.AckFailNotified, us...)
}

// LoadJournal returns the session journal, its lists normalized to sorted sets. A
// missing file gives an empty journal and no error; so does one of another
// version (or none); a broken one gives an empty journal and the error.
func LoadJournal(root, sessionID string) (Journal, error) {
	var j Journal
	data, err := fsutil.ReadFile(root, journalPath(root, sessionID), MaxSize)
	if errors.Is(err, os.ErrNotExist) {
		return Journal{}, nil
	}
	if err == nil {
		err = json.Unmarshal(data, &j)
	}
	if err != nil {
		return Journal{}, err
	}
	if j.Version != journalVersion {
		return Journal{}, nil
	}
	j.CodeChanged, j.DocsTouched, j.AlreadyNotified = AddUnique(j.CodeChanged), AddUnique(j.DocsTouched), AddUnique(j.AlreadyNotified)
	j.AckFailNotified = AddUnique(j.AckFailNotified)
	return j, nil
}

// AddCode adds units to the journal's code set; AddDocs and Notify do the same
// for the docs and notified sets. Use them rather than appending, so the lists
// stay sorted and unique.
func (j *Journal) AddCode(us ...string) { j.CodeChanged = AddUnique(j.CodeChanged, us...) }
func (j *Journal) AddDocs(us ...string) { j.DocsTouched = AddUnique(j.DocsTouched, us...) }
func (j *Journal) Notify(us ...string)  { j.AlreadyNotified = AddUnique(j.AlreadyNotified, us...) }

// Save writes the journal atomically; nil slices are stored as [].
func (j Journal) Save(root, sessionID string) error {
	// Written lists are always sorted sets, however j was built.
	set := func(l []string) []string { return append([]string{}, AddUnique(nil, l...)...) }
	j.CodeChanged, j.DocsTouched, j.AlreadyNotified = set(j.CodeChanged), set(j.DocsTouched), set(j.AlreadyNotified)
	j.AckFailNotified = AddUnique(nil, j.AckFailNotified...)
	j.Version = journalVersion
	data, err := json.Marshal(j)
	if err != nil {
		return err
	}
	return fsutil.WriteState(root, journalPath(root, sessionID), data)
}

// Clear removes the session's journal and snapshot; missing files are fine.
func Clear(root, sessionID string) error {
	var errs []error
	for _, p := range []string{journalPath(root, sessionID), SnapshotPath(root, sessionID)} {
		// Resolve first: a state dir linked elsewhere must not make this
		// delete a file outside it.
		rp, err := fsutil.Resolve(root, p)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if err := os.Remove(rp); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// AddUnique returns the sorted set union of list and items.
func AddUnique(list []string, items ...string) []string {
	out := append(slices.Clone(list), items...)
	slices.Sort(out)
	return slices.Compact(out)
}

// Minus returns the sorted set a minus every list in bs.
func Minus(a []string, bs ...[]string) []string {
	drop := map[string]bool{}
	for _, b := range bs {
		for _, x := range b {
			drop[x] = true
		}
	}
	var out []string
	for _, x := range a {
		if !drop[x] {
			out = append(out, x)
		}
	}
	return AddUnique(nil, out...)
}
