//go:build race

package rootindex

// raceOn reports a -race build: the race detector slows code down several
// times, so limit() lengthens the tests' wall-clock limits.
const raceOn = true
