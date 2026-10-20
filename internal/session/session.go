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
	"time"

	"auto-context/internal/fsutil"
	"auto-context/internal/units"
)

// Snapshot maps root-relative slash paths to their stat data.
type Snapshot map[string]units.Stat

// FromTree builds a snapshot from the stat data Discover already collected,
// so a hook does not stat every tracked file twice.
// The map is taken over, not copied: Tree.Stats already returns a copy.
func FromTree(stats map[string]units.Stat) Snapshot { return Snapshot(stats) }

// Diff returns sorted unique paths that changed, were removed or were added.
func Diff(before, after Snapshot) []string {
	var out []string
	for p, b := range before {
		if a, ok := after[p]; !ok || a != b {
			out = append(out, p)
		}
	}
	for p := range after {
		if _, ok := before[p]; !ok {
			out = append(out, p)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
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
// "<mtime_ns> <size> <path>\x00" (paths may contain spaces and newlines).
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
	s := Snapshot{}
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
		s[string(path)] = units.Stat{MtimeNs: m, Size: n}
	}
	return s, nil
}

// RemoveStale deletes every regular file in the state dir not modified within
// maxAge: files of sessions that ended without SessionEnd, the session mark of
// an abandoned project, leftover temporary files.
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
	var errs []error
	for _, e := range entries {
		fi, err := e.Info()
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				errs = append(errs, err)
			}
			continue
		}
		if !fi.Mode().IsRegular() || fi.ModTime().After(cutoff) {
			continue
		}
		if err := os.Remove(filepath.Join(dir, e.Name())); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Save writes the session's snapshot atomically.
func (s Snapshot) Save(root, sessionID string) error {
	buf := make([]byte, 0, len(snapshotHeader)+len(s)*48)
	buf = append(buf, snapshotHeader...)
	for p, st := range s {
		buf = strconv.AppendInt(buf, st.MtimeNs, 10)
		buf = append(buf, ' ')
		buf = strconv.AppendInt(buf, st.Size, 10)
		buf = append(buf, ' ')
		buf = append(buf, p...)
		buf = append(buf, 0)
	}
	return fsutil.WriteState(root, SnapshotPath(root, sessionID), buf)
}

// Journal records what happened during the current turn, across its Stop
// calls; PromptSubmit resets it.
type Journal struct {
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
func (j *Journal) NotifyAckFailed(units ...string) {
	j.AckFailNotified = AddUnique(j.AckFailNotified, units...)
}

// LoadJournal returns the session journal, its lists normalized to sorted sets. A
// missing file gives an empty journal and no error; a broken one gives an
// empty journal and the error.
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
	j.CodeChanged, j.DocsTouched, j.AlreadyNotified = AddUnique(j.CodeChanged), AddUnique(j.DocsTouched), AddUnique(j.AlreadyNotified)
	return j, nil
}

// AddCode, AddDocs and Notify add units to the journal's sets; use them
// rather than appending, so the lists stay sorted and unique.
func (j *Journal) AddCode(units ...string) { j.CodeChanged = AddUnique(j.CodeChanged, units...) }
func (j *Journal) AddDocs(units ...string) { j.DocsTouched = AddUnique(j.DocsTouched, units...) }
func (j *Journal) Notify(units ...string)  { j.AlreadyNotified = AddUnique(j.AlreadyNotified, units...) }

// Save writes the journal atomically; nil slices are stored as [].
func (j Journal) Save(root, sessionID string) error {
	// Written lists are always sorted sets, however j was built.
	set := func(l []string) []string { return append([]string{}, AddUnique(nil, l...)...) }
	j.CodeChanged, j.DocsTouched, j.AlreadyNotified = set(j.CodeChanged), set(j.DocsTouched), set(j.AlreadyNotified)
	j.AckFailNotified = AddUnique(nil, j.AckFailNotified...)
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
