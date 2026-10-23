//go:build !unix

package fsutil

import "os"

// readFlags is a plain read-only open where there are no FIFOs to block on.
const readFlags = os.O_RDONLY
