package fsutil

// ResetCaches clears the per-process root and name caches so a test starts
// from a clean state.
func ResetCaches() {
	realRoots.Clear()
	sameCache.Clear()
}
