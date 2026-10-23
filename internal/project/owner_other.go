//go:build !unix

package project

import "os"

func ownedByUser(os.FileInfo) bool { return true }
