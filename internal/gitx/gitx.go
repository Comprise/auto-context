// Package gitx runs git for the plugin under one fixed policy, so every call
// site gets the same protections against repo-controlled configuration.
package gitx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync/atomic"
	"time"
)

// Timeout bounds one git command. A variable so tests can lower it.
var Timeout = 20 * time.Second

// deadline (Unix nanoseconds, 0 = none) bounds all commands of the process.
var deadline atomic.Int64

// SetDeadline makes every later Run end by t: a hook's git calls together
// must fit in the hook's timeout, or Claude Code kills the hook first. The
// zero time removes the deadline.
func SetDeadline(t time.Time) {
	if t.IsZero() {
		deadline.Store(0)
		return
	}
	deadline.Store(t.UnixNano())
}

const maxStderr = 4 << 10

// Result is the output of one command.
type Result struct {
	Out       []byte
	Truncated bool // Out was cut at the limit
}

// Error is a git command that failed. Code is its exit status, or -1 when it
// did not exit normally: git could not start (NotRun), was killed after
// Timeout or at the deadline, or not run past it (TimedOut), or killed by a
// signal.
type Error struct {
	Args     []string
	Code     int
	Stderr   string
	NotRun   bool
	TimedOut bool
}

func (e *Error) Error() string {
	msg := fmt.Sprintf("git %s: exit %d", strings.Join(e.Args, " "), e.Code)
	if e.Stderr != "" {
		msg += ": " + e.Stderr
	}
	return msg
}

// Code returns the exit status of err from Run: 0 for nil, -1 when git did
// not exit normally.
func Code(err error) int {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	if err == nil {
		return 0
	}
	return -1
}

// Brief names a Run failure without git's own text (which may quote repo
// paths or config): "timed out", "could not be run" or "exit N".
func Brief(err error) string {
	var e *Error
	switch {
	case !errors.As(err, &e):
		return "failed"
	case e.TimedOut:
		return "timed out"
	case e.NotRun:
		return "could not be run"
	case e.Code >= 0:
		return fmt.Sprintf("exit %d", e.Code)
	}
	return "killed"
}

// NotRun reports whether err means git could not be started (e.g. it is not
// installed).
func NotRun(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.NotRun
}

// Run runs `git <args>` in dir and returns at most limit bytes of stdout
// (0 = no limit). fsmonitor and the pager are off, the system config is not
// read, git never prompts, speaks English (callers match its messages) and is
// killed after Timeout or at the deadline (see SetDeadline), whichever comes
// first. stderr (bounded) is part of the returned error.
func Run(dir string, limit int, args ...string) (Result, error) {
	wait := Timeout
	if d := deadline.Load(); d != 0 {
		wait = min(wait, time.Until(time.Unix(0, d)))
	}
	if wait <= 0 {
		return Result{}, &Error{Args: args, Code: -1, TimedOut: true, Stderr: "not run: the hook's time is up"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()
	full := append([]string{"-C", dir, "-c", "core.fsmonitor=false", "-c", "core.pager=cat", "--no-pager"}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Env = append(environ(), "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0", "GIT_CONFIG_NOSYSTEM=1",
		"LC_ALL=C", "LANGUAGE=C")
	var out, errb bytes.Buffer
	lw := &limitWriter{w: &out, n: limit}
	cmd.Stdout = lw
	cmd.Stderr = &limitWriter{w: &errb, n: maxStderr}
	err := cmd.Run()
	res := Result{Out: out.Bytes(), Truncated: lw.dropped}
	if err == nil {
		return res, nil
	}
	e := &Error{Args: args, Code: -1, Stderr: strings.TrimSpace(errb.String())}
	var ee *exec.ExitError
	switch {
	case ctx.Err() != nil:
		e.TimedOut = true
		e.Stderr = "timed out after " + wait.Round(time.Millisecond).String()
	case errors.As(err, &ee):
		e.Code = ee.ExitCode()
	default:
		e.NotRun = true
		e.Stderr = err.Error()
	}
	return res, e
}

// repoVars point git at another repository than the one around dir; the
// caller's values (e.g. set by a git hook) must not apply to the plugin's calls.
var repoVars = []string{"GIT_DIR=", "GIT_WORK_TREE=", "GIT_INDEX_FILE=", "GIT_OBJECT_DIRECTORY=",
	"GIT_ALTERNATE_OBJECT_DIRECTORIES=", "GIT_COMMON_DIR=", "GIT_NAMESPACE=", "GIT_PREFIX="}

// environ is the process environment without repoVars.
func environ() []string {
	var out []string
	for _, kv := range os.Environ() {
		if !slices.ContainsFunc(repoVars, func(p string) bool { return strings.HasPrefix(kv, p) }) {
			out = append(out, kv)
		}
	}
	return out
}

// limitWriter keeps the first n bytes (all if n is 0) and drops the rest.
type limitWriter struct {
	w       io.Writer
	n       int
	dropped bool
}

func (l *limitWriter) Write(p []byte) (int, error) {
	if l.n == 0 {
		return l.w.Write(p)
	}
	if l.n < 0 {
		l.dropped = l.dropped || len(p) > 0
		return len(p), nil
	}
	k := min(len(p), l.n)
	if k < len(p) {
		l.dropped = true
	}
	l.n -= k
	if l.n == 0 {
		l.n = -1
	}
	if _, err := l.w.Write(p[:k]); err != nil {
		return 0, err
	}
	return len(p), nil
}
