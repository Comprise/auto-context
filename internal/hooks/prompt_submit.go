package hooks

import (
	"errors"
	"os"

	"auto-context/internal/logx"
	"auto-context/internal/project"
	"auto-context/internal/session"
)

// PromptSubmit starts a turn: it drops any leftover journal and stores a fresh
// snapshot (from the stat data Discover already collected). Otherwise the
// snapshot from before a pause is kept, so the changes made meanwhile are
// reported: while the config is broken, on the first turn after it is fixed,
// and when the journal is unreadable (a pending pause cannot be ruled out).
func PromptSubmit(env Env, in Input) ([]byte, error) {
	if !project.Active(env.Root) {
		return nil, nil
	}
	root, id := env.Root, in.SessionID
	if !validSession(root, "prompt-submit", id) {
		return nil, nil
	}
	p, err := load(env)
	if err != nil {
		return nil, err
	}
	j, jerr := loadJournal(root, id, "prompt-submit")
	save := func() {
		if err := j.Save(root, id); err != nil {
			logx.Errorf(root, "prompt-submit: saving the journal: %v", err)
		}
	}
	if p.ConfigBroken() {
		// Keep the last good snapshot and journal: once the config is fixed,
		// Stop checks everything changed meanwhile. Without one (broken since
		// the session started) the first good turn takes a fresh snapshot.
		if !j.Paused {
			j.Pause()
			save()
		}
		return nil, nil
	}
	// Parsed only when it may be kept: it can be large.
	if (j.Paused || jerr != nil) && snapshotUsable(root, id) {
		// The config was fixed between turns (or the journal is unreadable, so
		// whether a pause is pending is unknown): keep the snapshot, so this
		// turn's Stop sees every change since it.
		j.Resume()
		save()
		return nil, nil
	}
	if err := errors.Join(session.Clear(root, id), removeSeenDocs(root, id)); err != nil {
		// The new snapshot below still replaces the old one.
		logx.Errorf(root, "prompt-submit: clearing the previous turn: %v", err)
	}
	return nil, session.FromTree(p.Stats()).Save(root, id)
}

// snapshotUsable reports whether the session's snapshot loads; an unreadable
// one is logged, as the fresh snapshot replacing it loses the paused changes.
func snapshotUsable(root, id string) bool {
	_, err := session.LoadSnapshot(root, id)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		logx.Errorf(root, "prompt-submit: snapshot unreadable, taking a fresh one (changes since it are not reported): %v", err)
	}
	return err == nil
}
