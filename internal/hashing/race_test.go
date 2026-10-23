//go:build race

package hashing

// raceOn reports a -race build: the race detector slows code down several
// times, so slack() widens the tests' wall-clock margins.
const raceOn = true
